package agent

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
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
