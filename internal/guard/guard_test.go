package guard

import "testing"

func TestBlocksMetadataAndLoopback(t *testing.T) {
	blocked := []string{
		"http://localhost/",
		"http://127.0.0.1/",
		"http://[::1]/",
		"http://10.0.0.5/",
		"http://192.168.1.10/",
		"http://172.16.4.4/",
		"http://169.254.169.254/latest/meta-data/",
		"http://metadata.google.internal/",
	}
	for _, u := range blocked {
		if err := CheckURL(u, nil, true); err == nil {
			t.Errorf("expected %s to be blocked", u)
		}
	}
}

func TestAllowsPublicAndLocalWhenOptedOut(t *testing.T) {
	if err := CheckURL("http://127.0.0.1:8080/", nil, false); err != nil {
		t.Errorf("loopback should be allowed when blockPrivate=false: %v", err)
	}
	if err := CheckURL("https://example.com/", nil, true); err != nil {
		t.Errorf("public host should be allowed: %v", err)
	}
}

func TestAllowlistRestrictsHosts(t *testing.T) {
	if err := CheckURL("https://evil.example.com/", []string{"example.com"}, true); err == nil {
		t.Error("expected non-allowlisted host to be rejected")
	}
	if err := CheckURL("https://example.com/", []string{"example.com"}, true); err != nil {
		t.Errorf("allowlisted host should pass: %v", err)
	}
}

func TestRejectsBadScheme(t *testing.T) {
	if err := CheckURL("file:///etc/passwd", nil, false); err == nil {
		t.Error("expected file scheme to be rejected")
	}
	if err := CheckURL("not a url", nil, false); err == nil {
		t.Error("expected malformed url to be rejected")
	}
}

// Non-http executors must get the same private-range and hostname policy as
// http; only the allowlist is http/https-specific.
func TestCheckDialTarget(t *testing.T) {
	wsSchemes := []string{"ws", "wss"}
	cases := []struct {
		name    string
		url     string
		bp      bool
		schemes []string
		ok      bool
	}{
		{"tcp loopback allowed", "tcp://127.0.0.1:9097", false, []string{"tcp"}, true},
		{"tcp public", "tcp://example.com:80", true, []string{"tcp"}, true},
		{"tcp link-local blocked", "tcp://169.254.169.254:80", true, []string{"tcp"}, false},
		{"tcp localhost name blocked", "tcp://localhost:80", false, []string{"tcp"}, false},
		{"tcp rfc1918 blocked", "tcp://10.0.0.5:80", true, []string{"tcp"}, false},
		{"ws public", "ws://example.com/socket", true, wsSchemes, true},
		{"wss public", "wss://example.com/socket", true, wsSchemes, true},
		{"ws loopback blocked", "ws://127.0.0.1/socket", true, wsSchemes, false},
		{"ws rfc1918 blocked", "ws://10.0.0.5/socket", true, wsSchemes, false},
		{"ws wrong scheme", "http://example.com", true, wsSchemes, false},
		{"grpc public", "grpc://example.com:9090", true, []string{"grpc"}, true},
		{"grpc loopback blocked", "grpc://127.0.0.1:9090", true, []string{"grpc"}, false},
	}
	for _, c := range cases {
		err := CheckDialTarget(c.url, c.bp, c.schemes...)
		if (err == nil) != c.ok {
			t.Errorf("%s: CheckDialTarget(%q, blockPrivate=%v) err=%v, want ok=%v",
				c.name, c.url, c.bp, err, c.ok)
		}
	}
}

func TestCheckDialTargetRejectsMalformed(t *testing.T) {
	if err := CheckDialTarget("", true, "tcp"); err == nil {
		t.Error("expected error for empty target")
	}
	if err := CheckDialTarget("tcp://", true, "tcp"); err == nil {
		t.Error("expected error for target with no host")
	}
	if err := CheckDialTarget("::::", true, "tcp"); err == nil {
		t.Error("expected error for malformed url")
	}
}

// The tcp executor accepts "host:port" as well as tcp://host:port.
func TestCheckHostPort(t *testing.T) {
	if err := CheckHostPort("127.0.0.1:9097", nil, false); err != nil {
		t.Errorf("loopback with blockPrivate=false: %v", err)
	}
	if err := CheckHostPort("127.0.0.1:9097", nil, true); err == nil {
		t.Error("expected loopback to be blocked")
	}
	if err := CheckHostPort("169.254.169.254:80", nil, true); err == nil {
		t.Error("expected link-local metadata to be blocked")
	}
	if err := CheckHostPort("example.com:443", nil, true); err != nil {
		t.Errorf("public host rejected: %v", err)
	}
	if err := CheckHostPort("example.com:443", []string{"other.com"}, true); err == nil {
		t.Error("expected allowlist rejection")
	}
	if err := CheckHostPort("nocolon", nil, false); err == nil {
		t.Error("expected error for missing port")
	}
}
