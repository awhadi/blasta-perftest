package privacy

import (
	"strings"
	"testing"

	"github.com/awhadi/blasta-perftest/internal/settings"
)

func TestCleanChecksTheSettings(t *testing.T) {
	if p, err := Clean(settings.Privacy{}); err != nil || p.Mode != "optin" {
		t.Errorf("empty settings should mean ask first: %+v %v", p, err)
	}
	for _, ok := range []string{"", "https://example.com/privacy", "http://example.com/p", "/policy"} {
		if _, err := Clean(settings.Privacy{Mode: "optout", PolicyURL: ok}); err != nil {
			t.Errorf("%q should be accepted: %v", ok, err)
		}
	}
	for name, p := range map[string]settings.Privacy{
		"unknown mode":      {Mode: "whatever"},
		"javascript url":    {Mode: "optin", PolicyURL: "javascript:alert(1)"},
		"protocol-relative": {Mode: "optin", PolicyURL: "//evil.example.com/x"},
		"quote in url":      {Mode: "optin", PolicyURL: `https://example.com/"><script>`},
		"credentials":       {Mode: "optin", PolicyURL: "https://u:p@example.com/"},
		"long message":      {Mode: "optin", Message: strings.Repeat("a", 501)},
		"long notes":        {Mode: "optin", Notes: strings.Repeat("a", 4001)},
	} {
		if _, err := Clean(p); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
}

func TestScriptFollowsTheModeAndNeverBecomesCode(t *testing.T) {
	if Script(settings.Privacy{Mode: "optin"}, "") != "" || Script(settings.Privacy{Mode: "optout"}, "") != "" {
		t.Error("with no analytics and no notice there is nothing to show")
	}
	if Script(settings.Privacy{Mode: "notice"}, "") == "" {
		t.Error("notice mode shows a banner even without analytics")
	}
	in := Script(settings.Privacy{Mode: "optin"}, "Plausible")
	for _, want := range []string{`"mode":"optin"`, "Plausible", `blasta_consent`, `if (choice === "1") loadAnalytics()`, `"Decline"`} {
		if !strings.Contains(in, want) {
			t.Errorf("opt-in script lacks %q", want)
		}
	}
	if !strings.Contains(in, `C.mode === "optin"`) {
		t.Error("the opt-in gate is missing")
	}
	// A message cannot break out of the script.
	evil := Script(settings.Privacy{Mode: "notice", Message: `</script><script>alert(1)</script>"; alert(2); //`}, "")
	if strings.Contains(evil, "</script>") || !strings.Contains(evil, `\"; alert(2)`) {
		t.Errorf("the banner text must be escaped: %.300s", evil)
	}
	if !strings.Contains(Script(settings.Privacy{Mode: "off"}, "Matomo"), `"mode":"off"`) {
		t.Error("off mode with analytics should still load it")
	}
}

func TestPageSaysWhatTheSiteDoes(t *testing.T) {
	base := PageData{Privacy: settings.Privacy{Mode: "optin", Controller: "Example Ltd", Contact: "privacy@example.test", Notes: "First.\n\nSecond <b>.", PolicyURL: "https://example.test/policy"}, Base: "./", Canonical: "https://s.test/privacy", SessionDays: 7, Guest: true}
	off := string(Page(base))
	for _, want := range []string{"Example Ltd", "privacy@example.test", "blasta_session", "blasta_guest", "up to 7 days", "<p>First.</p>", "Second &lt;b&gt;.", "https://example.test/policy", `<base href="./">`} {
		if !strings.Contains(strings.ReplaceAll(off, "Up to", "up to"), want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, want := range []string{"Templates you save", "Notification settings"} {
		if !strings.Contains(off, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(off, "Counting visits") || strings.Contains(off, "blasta_oidc_state") || strings.Contains(off, "Cookie settings") {
		t.Error("the page must not mention what is switched off")
	}
	base.AnalyticsName, base.SSO, base.Captcha = "Plausible", "Okta", "Cloudflare Turnstile"
	on := string(Page(base))
	for _, want := range []string{"Set by Plausible", "only if you accept", "blasta_oidc_state", "Set by Cloudflare Turnstile", "Cookie settings", "blasta_consent"} {
		if !strings.Contains(on, want) {
			t.Errorf("page lacks %q when switched on", want)
		}
	}
}
