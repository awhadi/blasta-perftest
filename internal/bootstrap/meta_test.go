package bootstrap

import "testing"

func TestMetaBool(t *testing.T) {
	m := map[string]any{"followRedirects": false, "insecureTLS": true, "junk": "yes"}
	if metaBool(m, "followRedirects", true) {
		t.Error("explicit false must override the true default")
	}
	if !metaBool(m, "insecureTLS", false) {
		t.Error("explicit true must be honoured")
	}
	if !metaBool(m, "junk", true) || metaBool(m, "missing", false) || !metaBool(nil, "x", true) {
		t.Error("non-bool or missing values must fall back to the default")
	}
}
