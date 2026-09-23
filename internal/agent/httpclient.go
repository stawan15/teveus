package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// On macOS, Go verifies TLS certificates through the system Security
// framework. On some macOS versions that framework occasionally fails before
// it even looks at the certificate ("SecPolicyCreateSSL error: 0"). When that
// happens we retry once with the same strict verification but against the CA
// bundle macOS ships in /etc/ssl/cert.pem, and keep using it afterwards.

var (
	useBundle      atomic.Bool
	bundleOnce     sync.Once
	bundleClient   *http.Client
	bundleClientOK bool
)

// caBundles are well-known CA files: macOS first, then common Linux paths.
var caBundles = []string{"/etc/ssl/cert.pem", "/etc/ssl/certs/ca-certificates.crt", "/etc/pki/tls/certs/ca-bundle.crt"}

func bundleHTTPClient() (*http.Client, bool) {
	bundleOnce.Do(func() {
		pool := x509.NewCertPool()
		for _, path := range caBundles {
			if pem, err := os.ReadFile(path); err == nil && pool.AppendCertsFromPEM(pem) {
				t := http.DefaultTransport.(*http.Transport).Clone()
				t.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
				bundleClient, bundleClientOK = &http.Client{Transport: t}, true
				return
			}
		}
	})
	return bundleClient, bundleClientOK
}

// isPlatformVerifierFailure spots errors from the macOS verifier itself, not
// from an actually untrusted certificate.
func isPlatformVerifierFailure(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "SecPolicyCreateSSL") || strings.Contains(s, "SecTrustCreateWithCertificates") ||
		strings.Contains(s, "SecTrustEvaluateWithError") && strings.Contains(s, "error: 0")
}

// httpDo sends a request, falling back to the CA bundle when the platform
// verifier breaks. Requests built with a bytes body can be replayed.
func httpDo(req *http.Request) (*http.Response, error) {
	if useBundle.Load() {
		if c, ok := bundleHTTPClient(); ok {
			return c.Do(req)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if !isPlatformVerifierFailure(err) {
		return resp, err
	}
	c, ok := bundleHTTPClient()
	if !ok {
		return resp, err
	}
	retry := req.Clone(req.Context())
	if req.GetBody != nil {
		body, berr := req.GetBody()
		if berr != nil {
			return resp, err
		}
		retry.Body = body
	}
	resp, err2 := c.Do(retry)
	if err2 == nil {
		useBundle.Store(true)
	}
	return resp, err2
}

// Retries: requests to the model (POSTs) that hit a rate limit (429),
// overload (529), a server error (5xx), or a dropped connection before any
// reply, are tried again after a pause,
// or after the Retry-After the server asks for (capped). Once a reply has
// started streaming nothing is retried: the model may already have acted.
var retryDelays = []time.Duration{2 * time.Second, 5 * time.Second, 12 * time.Second}

const maxRetryAfter = 60 * time.Second

type retryNoticeKey struct{}

// withRetryNotice lets the engine say "retrying in 5s" while it waits.
func withRetryNotice(ctx context.Context, f func(wait time.Duration, why string)) context.Context {
	return context.WithValue(ctx, retryNoticeKey{}, f)
}

func retryable(status int) bool {
	return status == 408 || status == 429 || status == 529 || (status >= 500 && status != 501)
}

// send makes a request, retrying failures that are worth retrying, and
// turns error replies into errors.
func send(ctx context.Context, method, url string, body []byte, header func(h http.Header)) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		var r io.Reader
		if body != nil {
			r = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, r)
		if err != nil {
			return nil, err
		}
		header(req.Header)
		resp, err := httpDo(req)
		var wait time.Duration
		var why string
		switch {
		case err != nil && ctx.Err() != nil:
			return nil, ctx.Err()
		case err != nil && (method != "POST" || errors.Is(err, syscall.ECONNREFUSED)):
			return nil, err // a listing, or nothing listening: waiting won't help
		case err != nil:
			why = "connection failed"
		case method != "POST" && resp.StatusCode >= 300:
			defer resp.Body.Close()
			return nil, apiError(resp)
		case retryable(resp.StatusCode):
			why = resp.Status
			if s, perr := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); perr == nil && s >= 0 {
				wait = min(time.Duration(s)*time.Second, maxRetryAfter)
			}
		case resp.StatusCode >= 300:
			defer resp.Body.Close()
			return nil, apiError(resp)
		default:
			return resp, nil
		}
		if attempt >= len(retryDelays) {
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			return nil, apiError(resp)
		}
		if resp != nil {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
		}
		if wait == 0 {
			wait = retryDelays[attempt]
		}
		if f, ok := ctx.Value(retryNoticeKey{}).(func(time.Duration, string)); ok {
			f(wait, why)
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
