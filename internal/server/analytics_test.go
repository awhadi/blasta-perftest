package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/awhadi/blasta-perftest/internal/auth"
	"github.com/awhadi/blasta-perftest/internal/bootstrap"
	"github.com/awhadi/blasta-perftest/internal/db"
	"github.com/awhadi/blasta-perftest/internal/settings"
	"github.com/awhadi/blasta-perftest/internal/ui"
)

// siteWithSettings is the whole site (pages, headers, API) with sign-in and saved settings.
func siteWithSettings(t *testing.T) *httptest.Server {
	t.Helper()
	bootstrap.Register()
	d, _ := db.OpenMemory()
	t.Cleanup(func() { d.Close() })
	svc, err := auth.New(auth.NewStore(d), auth.Config{Registration: auth.RegOpen})
	if err != nil {
		t.Fatal(err)
	}
	box, _ := settings.NewBox([]byte("0123456789abcdef-test-key"))
	if err := svc.UseSettings(settings.New(d, box)); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(NewServer("127.0.0.1:0", NewManager(log), ui.Assets(), log, WithAuth(svc)).http.Handler)
	t.Cleanup(srv.Close)
	return srv
}

type visitor struct {
	t   *testing.T
	url string
	c   *http.Client
}

func newVisitor(t *testing.T, url string) *visitor {
	jar, _ := cookiejar.New(nil)
	return &visitor{t: t, url: url, c: &http.Client{Jar: jar}}
}

func (v *visitor) do(method, path, body string) (int, http.Header, string) {
	req, _ := http.NewRequest(method, v.url+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "blasta")
	resp, err := v.c.Do(req)
	if err != nil {
		v.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

func TestAnalyticsIsOffUntilAnAdministratorTurnsItOn(t *testing.T) {
	srv := siteWithSettings(t)
	admin := newVisitor(t, srv.URL)
	if code, _, b := admin.do("POST", "/api/auth/register", `{"email":"admin@example.test","name":"A","password":"correct horse battery"}`); code != 201 {
		t.Fatalf("register: %d %s", code, b)
	}
	anon := newVisitor(t, srv.URL)

	// Off: no script, no tag, nothing extra allowed.
	if code, _, _ := anon.do("GET", "/analytics.js", ""); code != 404 {
		t.Errorf("/analytics.js while off = %d, want 404", code)
	}
	code, hdr, page := anon.do("GET", "/", "")
	if code != 200 || strings.Contains(page, "analytics.js") || strings.Contains(hdr.Get("Content-Security-Policy"), "googletagmanager") {
		t.Errorf("analytics must be invisible while off: %d", code)
	}

	// Only an administrator can change it, and a bad value is refused.
	if code, _, _ := anon.do("POST", "/api/admin/settings/analytics", `{"enabled":true,"provider":"ga4","id":"G-ABC123XYZ9"}`); code != 401 {
		t.Errorf("anonymous save = %d, want 401", code)
	}
	if code, _, b := admin.do("POST", "/api/admin/settings/analytics", `{"enabled":true,"provider":"ga4","id":"G-ABC\"); alert(1);//"}`); code != 400 {
		t.Errorf("a bad id must be refused: %d %s", code, b)
	}
	if code, _, b := admin.do("POST", "/api/admin/settings/analytics", `{"enabled":true,"provider":"ga4","id":"G-ABC123XYZ9","respectDnt":true}`); code != 200 {
		t.Fatalf("save: %d %s", code, b)
	}

	// On: the page loads the script, the policy lets Google through, and the script is served.
	code, hdr, page = anon.do("GET", "/", "")
	if !strings.Contains(page, `<script src="analytics.js" defer></script>`) {
		t.Error("the page should load analytics.js once it is on")
	}
	csp := hdr.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self' https://www.googletagmanager.com") || !strings.Contains(csp, "connect-src 'self' https://*.google-analytics.com") {
		t.Errorf("policy does not allow the provider: %s", csp)
	}
	if strings.Contains(csp, "script-src 'self' 'unsafe") {
		t.Error("analytics must never loosen the script policy beyond named hosts")
	}
	code, hdr, js := anon.do("GET", "/analytics.js", "")
	if code != 200 || !strings.Contains(hdr.Get("Content-Type"), "javascript") || !strings.Contains(js, `"G-ABC123XYZ9"`) || !strings.Contains(js, "doNotTrack") {
		t.Errorf("script: %d %s", code, js)
	}

	// People who are signed in are not counted unless the administrator says so.
	if _, _, js = admin.do("GET", "/analytics.js", ""); strings.Contains(js, "gtag") {
		t.Errorf("a signed-in person must not be counted by default: %s", js)
	}
	if code, _, b := admin.do("POST", "/api/admin/settings/analytics", `{"enabled":true,"provider":"ga4","id":"G-ABC123XYZ9","respectDnt":true,"trackSignedIn":true}`); code != 200 {
		t.Fatalf("save: %d %s", code, b)
	}
	if _, _, js = admin.do("GET", "/analytics.js", ""); !strings.Contains(js, "gtag") {
		t.Error("signed-in people are counted once the administrator allows it")
	}

	// The settings page can read it back, and switching it off removes everything again.
	if code, _, b := admin.do("GET", "/api/admin/settings", ""); code != 200 || !strings.Contains(b, `"analytics":{"enabled":true,"provider":"ga4","id":"G-ABC123XYZ9"`) || !strings.Contains(b, `"analyticsProviders"`) {
		t.Errorf("settings view: %d %s", code, b)
	}
	if code, _, b := admin.do("POST", "/api/admin/settings/analytics", `{"enabled":false,"provider":"ga4","id":"G-ABC123XYZ9"}`); code != 200 {
		t.Fatalf("turn off: %d %s", code, b)
	}
	if code, _, _ := anon.do("GET", "/analytics.js", ""); code != 404 {
		t.Errorf("/analytics.js after turning it off = %d, want 404", code)
	}
	if _, hdr, page = anon.do("GET", "/", ""); strings.Contains(page, "analytics.js") || strings.Contains(hdr.Get("Content-Security-Policy"), "googletagmanager") {
		t.Error("turning it off must remove the tag and the allowed hosts")
	}
	// Reset forgets what was saved.
	if code, _, b := admin.do("DELETE", "/api/admin/settings/analytics", ""); code != 200 {
		t.Errorf("reset: %d %s", code, b)
	}
}
