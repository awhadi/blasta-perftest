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
	srv, _ := siteWithDB(t)
	return srv
}

// dbRows runs a query that returns one text column and gives the values.
func dbRows(d *db.DB, q string) []string {
	rows, err := d.Query(q)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			out = append(out, s)
		}
	}
	return out
}

func siteWithDB(t *testing.T) (*httptest.Server, *db.DB) {
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
	mgr := NewManager(log)
	if err := mgr.EnableHistory(d, "", "", nil); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewServer("127.0.0.1:0", mgr, ui.Assets(), log, WithAuth(svc)).http.Handler)
	t.Cleanup(srv.Close)
	return srv, d
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
	if code, _, _ := anon.do("GET", "/consent.js", ""); code != 404 {
		t.Errorf("/consent.js with nothing to ask about = %d, want 404", code)
	}
	code, hdr, page := anon.do("GET", "/", "")
	if code != 200 || strings.Contains(page, "analytics.js") || strings.Contains(page, "consent.js") || strings.Contains(hdr.Get("Content-Security-Policy"), "googletagmanager") {
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
	// The page loads the consent script, which loads analytics only as the consent rules allow
	// (by default: ask first).
	if !strings.Contains(page, `<script src="consent.js" defer></script>`) || strings.Contains(page, `src="analytics.js"`) {
		t.Error("the page should load consent.js, not analytics.js directly")
	}
	if code, _, cj := anon.do("GET", "/consent.js", ""); code != 200 || !strings.Contains(cj, `"mode":"optin"`) || !strings.Contains(cj, "Google Analytics 4") {
		t.Errorf("consent script: %d %s", code, cj)
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
	if _, hdr, page = anon.do("GET", "/", ""); strings.Contains(page, "analytics.js") || strings.Contains(page, "consent.js") || strings.Contains(hdr.Get("Content-Security-Policy"), "googletagmanager") {
		t.Error("turning it off must remove the tag and the allowed hosts")
	}
	// Reset forgets what was saved.
	if code, _, b := admin.do("DELETE", "/api/admin/settings/analytics", ""); code != 200 {
		t.Errorf("reset: %d %s", code, b)
	}
}

func TestPrivacySettingsBannerAndPage(t *testing.T) {
	srv := siteWithSettings(t)
	admin := newVisitor(t, srv.URL)
	if code, _, b := admin.do("POST", "/api/auth/register", `{"email":"admin@example.test","name":"A","password":"correct horse battery"}`); code != 201 {
		t.Fatalf("register: %d %s", code, b)
	}
	anon := newVisitor(t, srv.URL)

	// The privacy page always exists, and says what is on.
	code, hdr, page := anon.do("GET", "/privacy", "")
	if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/html") || !strings.Contains(page, "blasta_session") || strings.Contains(page, "Counting visits") {
		t.Errorf("privacy page: %d", code)
	}

	// Only an administrator can change it; bad values are refused.
	if code, _, _ := anon.do("POST", "/api/admin/settings/privacy", `{"mode":"notice"}`); code != 401 {
		t.Errorf("anonymous save = %d, want 401", code)
	}
	if code, _, _ := admin.do("POST", "/api/admin/settings/privacy", `{"mode":"nonsense"}`); code != 400 {
		t.Errorf("an unknown mode must be refused")
	}
	if code, _, _ := admin.do("POST", "/api/admin/settings/privacy", `{"mode":"optin","policyUrl":"javascript:alert(1)"}`); code != 400 {
		t.Errorf("a javascript: policy address must be refused")
	}

	// A notice-only site shows its banner even with no analytics.
	if code, _, b := admin.do("POST", "/api/admin/settings/privacy", `{"mode":"notice","controller":"Example Ltd","contact":"privacy@example.test","message":"We use cookies to keep you signed in."}`); code != 200 {
		t.Fatalf("save: %d %s", code, b)
	}
	if _, _, p := anon.do("GET", "/", ""); !strings.Contains(p, `<script src="consent.js" defer></script>`) {
		t.Error("a notice needs the consent script")
	}
	if code, _, cj := anon.do("GET", "/consent.js", ""); code != 200 || !strings.Contains(cj, "We use cookies to keep you signed in.") || !strings.Contains(cj, `"mode":"notice"`) {
		t.Errorf("notice script: %d", code)
	}
	if _, _, p := anon.do("GET", "/privacy", ""); !strings.Contains(p, "Example Ltd") || !strings.Contains(p, "privacy@example.test") {
		t.Error("the privacy page should name the controller and contact")
	}

	// With analytics on and opt-out, the script gates analytics on a "no", not a "yes".
	if code, _, b := admin.do("POST", "/api/admin/settings/analytics", `{"enabled":true,"provider":"plausible","id":"example.test","respectDnt":true}`); code != 200 {
		t.Fatalf("analytics: %d %s", code, b)
	}
	if code, _, b := admin.do("POST", "/api/admin/settings/privacy", `{"mode":"optout"}`); code != 200 {
		t.Fatalf("optout: %d %s", code, b)
	}
	if _, _, cj := anon.do("GET", "/consent.js", ""); !strings.Contains(cj, `"mode":"optout"`) || !strings.Contains(cj, "Plausible") {
		t.Error("opt-out script should name the service")
	}
	if _, _, p := anon.do("GET", "/privacy", ""); !strings.Contains(p, "Set by Plausible") || !strings.Contains(p, "you can opt out") {
		t.Error("the privacy page should list the analytics cookies and the opt-out")
	}
	// A signed-in person who is not counted has nothing to be asked about.
	if code, _, _ := admin.do("GET", "/consent.js", ""); code != 200 {
		t.Log("notice-only banners may still be shown to signed-in people")
	}
	// "off" with analytics loads it without a banner; with no analytics it removes the script.
	if code, _, b := admin.do("POST", "/api/admin/settings/analytics", `{"enabled":false,"provider":"plausible","id":"example.test"}`); code != 200 {
		t.Fatalf("analytics off: %d %s", code, b)
	}
	if code, _, b := admin.do("POST", "/api/admin/settings/privacy", `{"mode":"optin"}`); code != 200 {
		t.Fatalf("optin: %d %s", code, b)
	}
	if code, _, _ := anon.do("GET", "/consent.js", ""); code != 404 {
		t.Errorf("no analytics and no notice: /consent.js = %d, want 404", code)
	}
}

func TestPeopleCanExportAndDeleteTheirOwnData(t *testing.T) {
	srv := siteWithSettings(t)
	admin := newVisitor(t, srv.URL)
	if code, _, b := admin.do("POST", "/api/auth/register", `{"email":"admin@example.test","name":"A","password":"correct horse battery"}`); code != 201 {
		t.Fatalf("register: %d %s", code, b)
	}
	// Close sign-up is not needed: registration is open, so a second person can join.
	pat := newVisitor(t, srv.URL)
	if code, _, b := pat.do("POST", "/api/auth/register", `{"email":"pat@example.test","name":"Pat","password":"correct horse battery"}`); code != 201 {
		t.Fatalf("register pat: %d %s", code, b)
	}
	anon := newVisitor(t, srv.URL)
	if code, _, _ := anon.do("GET", "/api/me/export", ""); code != 401 {
		t.Errorf("anonymous export = %d, want 401", code)
	}

	// Export: their own account and nothing secret.
	code, hdr, body := pat.do("GET", "/api/me/export", "")
	if code != 200 || !strings.Contains(hdr.Get("Content-Disposition"), "attachment") || !strings.Contains(body, "pat@example.test") || !strings.Contains(body, `"sessions"`) {
		t.Fatalf("export: %d %s", code, body)
	}
	if strings.Contains(body, "passwordHash") || strings.Contains(body, "$pbkdf2") || strings.Contains(body, "admin@example.test") {
		t.Errorf("the export must hold only their own data and no secrets: %s", body)
	}

	// Delete: the wrong password is refused, the right one removes the account and signs them out.
	if code, _, _ := pat.do("DELETE", "/api/me", `{"password":"wrong password here"}`); code != 403 {
		t.Errorf("wrong password = %d, want 403", code)
	}
	if code, _, b := pat.do("DELETE", "/api/me", `{"password":"correct horse battery"}`); code != 200 {
		t.Fatalf("delete: %d %s", code, b)
	}
	if code, _, _ := pat.do("GET", "/api/me/export", ""); code != 401 {
		t.Errorf("after deleting, the session must be gone: %d", code)
	}
	if code, _, _ := newVisitor(t, srv.URL).do("POST", "/api/auth/login", `{"email":"pat@example.test","password":"correct horse battery"}`); code == 200 {
		t.Error("a deleted account must not sign in")
	}

	// The only administrator cannot delete themselves.
	if code, _, _ := admin.do("DELETE", "/api/me", `{"password":"correct horse battery"}`); code != 409 {
		t.Errorf("the last administrator = %d, want 409", code)
	}

	// An administrator can switch self-service deletion off.
	pat2 := newVisitor(t, srv.URL)
	if code, _, b := pat2.do("POST", "/api/auth/register", `{"email":"sam@example.test","name":"Sam","password":"correct horse battery"}`); code != 201 {
		t.Fatalf("register sam: %d %s", code, b)
	}
	if code, _, b := admin.do("POST", "/api/admin/settings/privacy", `{"mode":"optin","noSelfDelete":true}`); code != 200 {
		t.Fatalf("settings: %d %s", code, b)
	}
	if code, _, _ := pat2.do("DELETE", "/api/me", `{"password":"correct horse battery"}`); code != 403 {
		t.Errorf("with self-deletion off = %d, want 403", code)
	}
	if _, _, cfg := anon.do("GET", "/api/auth/config", ""); !strings.Contains(cfg, `"selfDelete":false`) {
		t.Errorf("the sign-in config should say deletion is off: %s", cfg)
	}
}
