package agent

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// WebFetch is a tool the model calls with a URL it chose, so the URL is
// untrusted input: a page it read can tell it to fetch
// http://169.254.169.254/latest/meta-data/iam/security-credentials/ (cloud
// metadata), a router's admin page on 192.168.1.1, or teveus's own phone page
// on 127.0.0.1:8787. None of that is the open web, so it is refused unless the
// user turned local fetching on with WebFetchLocal.

// allowLocalFetch is set from settings; it is a package variable so the tool
// function (which has no access to the model) can read it.
var allowLocalFetch bool

// SetWebFetchLocal lets WebFetch reach loopback and private addresses. It is
// off by default and the UI exposes it as an explicit opt-in.
func SetWebFetchLocal(v bool) { allowLocalFetch = v }

// localFetchAllowed reports whether a URL may reach this machine or its LAN.
func localFetchAllowed() bool { return allowLocalFetch }

// ipIsLocal reports whether an address is loopback, private, link-local,
// CGNAT, multicast or otherwise not part of the public internet.
func ipIsLocal(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		// 100.64.0.0/10 CGNAT (used by Tailscale too), 169.254.0.0/16 metadata,
		// 0.0.0.0/8, 192.0.0.0/24, 198.18.0.0/15 benchmarking.
		switch {
		case ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127:
			return true
		case ip4[0] == 169 && ip4[1] == 254:
			return true
		case ip4[0] == 0:
			return true
		case ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 0:
			return true
		case ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19):
			return true
		}
		return false
	}
	// IPv6: unique local fc00::/7, plus IPv4-mapped addresses (caught above
	// after To4, but check the raw form too).
	if len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc {
		return true
	}
	return false
}

// hostIsLocal resolves a host name and reports whether it points anywhere
// local. A name that doesn't resolve is not local: the fetch will fail on its
// own and the error is more useful than a security message.
func hostIsLocal(ctx context.Context, host string) bool {
	h := host
	if hp, _, err := net.SplitHostPort(host); err == nil {
		h = hp
	}
	h = strings.Trim(h, "[]")
	if h == "" {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ipIsLocal(ip)
	}
	// "localhost" and its friends never leave the machine.
	lower := strings.ToLower(strings.TrimSuffix(h, "."))
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") || strings.HasSuffix(lower, ".local") || lower == "metadata" {
		return true
	}
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(rctx, h)
	if err != nil || len(addrs) == 0 {
		return false
	}
	for _, a := range addrs {
		if !ipIsLocal(a.IP) {
			return false // one public address is enough to allow it
		}
	}
	return true
}

// checkFetchURL refuses URLs that point at this machine or its network. It
// runs before the request and again on every redirect.
func checkFetchURL(ctx context.Context, u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http(s) URLs can be fetched")
	}
	if localFetchAllowed() {
		return nil
	}
	if hostIsLocal(ctx, u.Host) {
		return fmt.Errorf("%s is on this machine or its local network, which WebFetch doesn't reach "+
			"(turn on local fetching in settings if you need it)", u.Redacted())
	}
	return nil
}

// fetchClient is used by WebFetch. It refuses local destinations on the
// first request and on each redirect, which is where a public URL usually
// turns into 127.0.0.1.
func fetchClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("stopped after 5 redirects")
			}
			return checkFetchURL(req.Context(), req.URL)
		},
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
				// Ask the kernel for the address we actually connected to, so a
				// DNS answer that changes between the check and the dial (DNS
				// rebinding) can't slip a loopback address through.
				Control: func(network, address string, _ syscall.RawConn) error {
					if localFetchAllowed() {
						return nil
					}
					host, _, err := net.SplitHostPort(address)
					if err != nil {
						return err
					}
					if ip := net.ParseIP(host); ip != nil && ipIsLocal(ip) {
						return fmt.Errorf("%s is on this machine or its local network", host)
					}
					return nil
				},
			}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 20 * time.Second,
			MaxIdleConns:          10,
			IdleConnTimeout:       30 * time.Second,
		},
	}
}
