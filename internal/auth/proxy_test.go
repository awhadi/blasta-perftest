package auth

import (
	"net"
	"net/http/httptest"
	"testing"
)

func cidrs(t *testing.T, list ...string) []*net.IPNet {
	t.Helper()
	var out []*net.IPNet
	for _, c := range list {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

// X-Forwarded-For is believed only from a proxy we were told to trust.
func TestClientIPBehindAProxy(t *testing.T) {
	r := func(remote, xff, real string) string {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = remote
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		if real != "" {
			req.Header.Set("X-Real-IP", real)
		}
		return req.RemoteAddr + "|" + func() string {
			s := newSvc(t, Config{TrustedProxies: cidrs(t, "10.0.0.0/8")})
			return s.clientIP(req)
		}()
	}
	cases := []struct{ name, remote, xff, real, want string }{
		{"a trusted proxy reports the client", "10.0.0.5:4000", "203.0.113.7", "", "203.0.113.7"},
		{"the rightmost untrusted hop wins, not what the client forged", "10.0.0.5:4000", "1.1.1.1, 203.0.113.7, 10.0.0.9", "", "203.0.113.7"},
		{"an untrusted peer cannot forge it", "198.51.100.9:4000", "1.2.3.4", "", "198.51.100.9"},
		{"no header: the proxy's own address", "10.0.0.5:4000", "", "", "10.0.0.5"},
		{"X-Real-IP is the fallback", "10.0.0.5:4000", "", "203.0.113.8", "203.0.113.8"},
		{"garbage is ignored", "10.0.0.5:4000", "not-an-ip", "", "10.0.0.5"},
	}
	for _, c := range cases {
		got := r(c.remote, c.xff, c.real)
		if want := c.remote + "|" + c.want; got != want {
			t.Errorf("%s: got %s, want %s", c.name, got, want)
		}
	}
	// With nothing trusted, headers are never believed.
	s := newSvc(t, Config{})
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.5:1"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := s.clientIP(req); got != "10.0.0.5" {
		t.Errorf("no trusted proxies configured: got %s", got)
	}
}

func TestCleanBase(t *testing.T) {
	for in, want := range map[string]string{"": "", "/": "", "blasta": "/blasta", "/blasta/": "/blasta", "/a/b": "/a/b",
		"/bl ast": "", "/<script>": "", "/..": "", "//x": "", "/a//b": "", "/blasta?x=1": ""} {
		if got := CleanBase(in); got != want {
			t.Errorf("CleanBase(%q) = %q, want %q", in, got, want)
		}
	}
}

// The address given to the identity provider follows the Public URL, including a
// path, and otherwise what a proxy says it published.
func TestRedirectURL(t *testing.T) {
	mk := func(public string, hdr map[string]string) string {
		s := newSvc(t, Config{PublicURL: public})
		r := httptest.NewRequest("GET", "/api/auth/oidc/start", nil)
		r.Host = "blasta:8080"
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		return s.redirectURL(r)
	}
	if got := mk("https://blasta.example.com/", nil); got != "https://blasta.example.com/api/auth/oidc/callback" {
		t.Error(got)
	}
	if got := mk("https://example.com/blasta", nil); got != "https://example.com/blasta/api/auth/oidc/callback" {
		t.Error(got)
	}
	if got := mk("", map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-Host": "blasta.example.com", "X-Forwarded-Prefix": "/tools/blasta"}); got != "https://blasta.example.com/tools/blasta/api/auth/oidc/callback" {
		t.Error(got)
	}
	if got := mk("", map[string]string{"X-Forwarded-Prefix": "/<script>"}); got != "http://blasta:8080/api/auth/oidc/callback" {
		t.Errorf("a malicious prefix must be ignored: %s", got)
	}
	if got := mk("", nil); got != "http://blasta:8080/api/auth/oidc/callback" {
		t.Error(got)
	}
	if p := newSvc(t, Config{PublicURL: "https://example.com/blasta/"}).PublicPath(); p != "/blasta" {
		t.Errorf("PublicPath = %q", p)
	}
}
