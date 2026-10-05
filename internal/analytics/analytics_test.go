package analytics

import (
	"strings"
	"testing"

	"github.com/awhadi/blasta-perftest/internal/settings"
)

func on(provider, id, url string) settings.Analytics {
	return settings.Analytics{Enabled: true, Provider: provider, ID: id, ScriptURL: url, RespectDNT: true}
}

func TestCleanAcceptsEachProvider(t *testing.T) {
	for _, c := range []settings.Analytics{
		on("ga4", "G-ABC123XYZ9", ""),
		on("gtm", "GTM-ABC123", ""),
		on("plausible", "example.com", ""),
		on("plausible", "example.com", "https://stats.example.org/"),
		on("umami", "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", ""),
		on("umami", "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", "https://umami.example.com/script.js"),
		on("matomo", "3", "https://matomo.example.com/"),
		on("cloudflare", "0123456789abcdef0123456789abcdef", ""),
		on("custom", "", "https://cdn.example.com/a.js"),
	} {
		if _, err := Clean(c); err != nil {
			t.Errorf("%s %q: %v", c.Provider, c.ID, err)
		}
		if Script(c) == "" {
			t.Errorf("%s: no script for valid settings", c.Provider)
		}
	}
}

func TestCleanRefusesWhatCouldBecomeCode(t *testing.T) {
	for name, c := range map[string]settings.Analytics{
		"ga4 with a quote":           on("ga4", `G-ABC"); alert(1);//`, ""),
		"ga4 lowercase":              on("ga4", "g-abc123xyz9", ""),
		"gtm wrong":                  on("gtm", "GTM-", ""),
		"plausible domain with path": on("plausible", "example.com/x", ""),
		"plausible http host":        on("plausible", "example.com", "http://stats.example.org"),
		"umami not an id":            on("umami", "abc", ""),
		"umami javascript url":       on("umami", "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", "javascript:alert(1)"),
		"matomo no address":          on("matomo", "3", ""),
		"matomo id with letters":     on("matomo", "3a", "https://m.example.com"),
		"cloudflare short":           on("cloudflare", "abc", ""),
		"custom http":                on("custom", "", "http://cdn.example.com/a.js"),
		"custom with quote":          on("custom", "", `https://cdn.example.com/a.js"></script><script>x`),
		"custom with credentials":    on("custom", "", "https://user:pw@cdn.example.com/a.js"),
		"unknown provider":           on("tracker9000", "x", ""),
	} {
		if _, err := Clean(c); err == nil {
			t.Errorf("%s must be refused", name)
		}
		if Script(c) != "" {
			t.Errorf("%s: no script may be produced for invalid settings", name)
		}
	}
	if _, err := Clean(settings.Analytics{Enabled: true, Provider: "ga4", ID: "G-ABC123XYZ9", ExtraHosts: []string{"evil.com/path?x"}}); err == nil {
		t.Error("an extra host with a path must be refused")
	}
	many := settings.Analytics{Enabled: true, Provider: "ga4", ID: "G-ABC123XYZ9"}
	for i := 0; i < maxExtraHosts+1; i++ {
		many.ExtraHosts = append(many.ExtraHosts, "h.example.com")
	}
	if _, err := Clean(many); err == nil {
		t.Error("too many extra hosts must be refused")
	}
}

func TestOffMeansNothingIsLoadedOrAllowed(t *testing.T) {
	off := on("ga4", "G-ABC123XYZ9", "")
	off.Enabled = false
	if Script(off) != "" || Ready(off) {
		t.Error("a disabled configuration must produce no script")
	}
	if s, c, i := CSP(off); len(s)+len(c)+len(i) != 0 {
		t.Errorf("a disabled configuration must allow nothing: %v %v %v", s, c, i)
	}
	// Half-filled settings can be saved while it is off.
	if _, err := Clean(settings.Analytics{Provider: "ga4", ID: "not yet"}); err != nil {
		t.Errorf("a disabled form may be incomplete: %v", err)
	}
}

func TestScriptsAreSafeAndRespectPrivacy(t *testing.T) {
	ga := Script(on("ga4", "G-ABC123XYZ9", ""))
	for _, want := range []string{`"G-ABC123XYZ9"`, "googletagmanager.com/gtag/js?id=", "doNotTrack", "globalPrivacyControl", "token|code|state"} {
		if !strings.Contains(ga, want) {
			t.Errorf("ga4 script lacks %q:\n%s", want, ga)
		}
	}
	c := on("ga4", "G-ABC123XYZ9", "")
	c.RespectDNT = false
	if strings.Contains(Script(c), "doNotTrack") {
		t.Error("Do Not Track is only honoured when asked for")
	}
	if !strings.Contains(Script(on("plausible", "example.com", "")), `"data-domain": "example.com"`) {
		t.Error("plausible script lacks its domain")
	}
	cf := Script(on("cloudflare", "0123456789abcdef0123456789abcdef", ""))
	if !strings.Contains(cf, `"{\"token\":\"0123456789abcdef0123456789abcdef\"}"`) {
		t.Errorf("cloudflare beacon attribute is not a JSON string: %s", cf)
	}
	if !strings.Contains(Script(on("matomo", "3", "https://matomo.example.com/")), `"https://matomo.example.com"`) {
		t.Error("matomo script lacks its address")
	}
}

func TestCSPAllowsOnlyWhatTheProviderNeeds(t *testing.T) {
	s, c, i := CSP(on("plausible", "example.com", ""))
	if len(s) != 1 || s[0] != "https://plausible.io" || len(c) != 1 || len(i) != 0 {
		t.Errorf("plausible: %v %v %v", s, c, i)
	}
	s, _, _ = CSP(on("umami", "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", "https://umami.example.com/deep/script.js"))
	if len(s) != 1 || s[0] != "https://umami.example.com" {
		t.Errorf("umami must allow only its host: %v", s)
	}
	s, c, i = CSP(on("ga4", "G-ABC123XYZ9", ""))
	if len(s) != 1 || s[0] != "https://www.googletagmanager.com" || len(c) == 0 || len(i) == 0 {
		t.Errorf("ga4: %v %v %v", s, c, i)
	}
	extra := on("custom", "", "https://cdn.example.com/a.js")
	extra.ExtraHosts = []string{"api.example.com"}
	s, c, _ = CSP(extra)
	if len(s) != 2 || s[1] != "https://api.example.com" || len(c) != 2 {
		t.Errorf("custom with an extra host: %v %v", s, c)
	}
	for _, h := range s {
		if strings.Contains(h, "*") && !strings.HasPrefix(h, "https://*.") {
			t.Errorf("wildcards only as subdomains: %s", h)
		}
	}
}
