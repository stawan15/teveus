package agent

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlatformVerifierFailureDetection(t *testing.T) {
	for msg, want := range map[string]bool{
		`Get "https://x/models": tls: failed to verify certificate: SecPolicyCreateSSL error: 0`: true,
		`tls: failed to verify certificate: x509: certificate signed by unknown authority`:       false,
		`dial tcp: connection refused`: false,
	} {
		if got := isPlatformVerifierFailure(errors.New(msg)); got != want {
			t.Errorf("%q: got %v", msg, got)
		}
	}
}

func TestBundleClientVerifiesRealCertificates(t *testing.T) {
	c, ok := bundleHTTPClient()
	if !ok {
		t.Skip("no CA bundle on this system")
	}
	// A self-signed test server must still be rejected: the fallback keeps
	// verification strict.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hi") }))
	defer srv.Close()
	if _, err := c.Get(srv.URL); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("self-signed cert accepted: %v", err)
	}
}
