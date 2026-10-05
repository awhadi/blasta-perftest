package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/awhadi/blasta-perftest/internal/settings"
)

// fakeProvider answers siteverify: a token "good" passes, a secret "badsecret" is
// rejected as a key, anything else is a bad token.
func fakeProvider(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		out := map[string]any{"success": false, "error-codes": []string{"invalid-input-response"}}
		switch {
		case r.PostForm.Get("secret") == "badsecret":
			out["error-codes"] = []string{"invalid-input-secret"}
		case r.PostForm.Get("response") == "good":
			out = map[string]any{"success": true}
		}
		json.NewEncoder(w).Encode(out)
	}))
	old := captchaEndpoints["turnstile"]
	captchaEndpoints["turnstile"] = srv.URL
	t.Cleanup(func() { captchaEndpoints["turnstile"] = old; srv.Close() })
}

func protected() settings.Captcha {
	return settings.Captcha{Enabled: true, Provider: "turnstile", SiteKey: "site", Secret: "secret", OnLogin: true, OnRegister: true, OnGuest: true}
}

func TestBotCheckProtectsSignInAndRegistration(t *testing.T) {
	fakeProvider(t)
	e := newSSOEnv(t, func(c *Config, _ *OIDCConfig) { c.Captcha = protected(); c.Registration = RegOpen })
	post := func(path, body string) (int, map[string]any) {
		resp, err := http.Post(e.srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		return resp.StatusCode, m
	}
	// The first account (the administrator) is never challenged: nobody could set up.
	if code, m := post("/api/auth/register", `{"email":"admin@x.test","password":"`+goodPW+`"}`); code != 201 {
		t.Fatalf("the first account must not need the check: %d %v", code, m)
	}
	for name, body := range map[string]string{
		"no token":    `{"email":"new@x.test","password":"` + goodPW + `"}`,
		"a bad token": `{"email":"new@x.test","password":"` + goodPW + `","captcha":"nope"}`,
	} {
		if code, m := post("/api/auth/register", body); code != 400 || m["code"] != "captcha" {
			t.Errorf("registration %s: %d %v", name, code, m)
		}
	}
	if code, m := post("/api/auth/register", `{"email":"new@x.test","password":"`+goodPW+`","captcha":"good"}`); code != 201 {
		t.Errorf("a good token registers: %d %v", code, m)
	}
	// Sign-in, password reset and resend ask for it too, before anything else happens.
	if code, m := post("/api/auth/login", `{"email":"admin@x.test","password":"`+goodPW+`"}`); code != 400 || m["code"] != "captcha" {
		t.Errorf("login without a token: %d %v", code, m)
	}
	if code, _ := post("/api/auth/login", `{"email":"admin@x.test","password":"`+goodPW+`","captcha":"good"}`); code != 200 {
		t.Errorf("login with a token: %d", code)
	}
	for _, p := range []string{"/api/auth/forgot", "/api/auth/resend"} {
		if code, m := post(p, `{"email":"admin@x.test"}`); code != 400 || m["code"] != "captcha" {
			t.Errorf("%s without a token: %d %v", p, code, m)
		}
	}
	// The page learns about it, without the secret.
	b, _ := json.Marshal(e.svc.PublicConfig())
	if !strings.Contains(string(b), `"siteKey":"site"`) || strings.Contains(string(b), "secret") {
		t.Errorf("public config: %s", b)
	}
}

func TestBotCheckIsOffWhenNotConfiguredOrSwitchedOff(t *testing.T) {
	fakeProvider(t)
	if newSvc(t, Config{}).CaptchaRequired(ScopeLogin) {
		t.Error("nothing configured: nothing required")
	}
	half := protected()
	half.Secret = ""
	if newSvc(t, Config{Captcha: half}).CaptchaRequired(ScopeLogin) {
		t.Error("a half-configured check must not lock people out")
	}
	partial := protected()
	partial.OnLogin = false
	s := newSvc(t, Config{Captcha: partial})
	if s.CaptchaRequired(ScopeLogin) || !s.CaptchaRequired(ScopeRegister) {
		t.Error("each place is switched separately")
	}
	// The emergency switch beats saved settings.
	e := newAdminEnv(t)
	e.svc.base.CaptchaOff = true
	e.svc.settings.PutCaptcha(protected())
	e.svc.Reload()
	if e.svc.CaptchaRequired(ScopeLogin) {
		t.Error("BLASTA_CAPTCHA_OFF must switch the check off")
	}
}

func TestVisitorsPassTheBotCheckOnceBeforeTheirFirstTest(t *testing.T) {
	fakeProvider(t)
	s := newSvc(t, Config{Captcha: protected()})
	rec := httptest.NewRecorder()
	g := s.guestFor(rec, httptest.NewRequest("POST", "/api/jobs", nil), true)
	if g == nil || !s.GuestNeedsCheck(g) {
		t.Fatalf("a new visitor must be asked: %+v", g)
	}
	cookie := rec.Result().Cookies()[0]
	call := func(token string) int {
		r := httptest.NewRequest("POST", "/api/auth/guest/captcha", strings.NewReader(`{"captcha":"`+token+`"}`))
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		s.handleGuestCaptcha(w, r)
		return w.Code
	}
	if call("nope") != 400 {
		t.Error("a bad token must be refused")
	}
	again := func() *Guest {
		r := httptest.NewRequest("GET", "/api/runs", nil)
		r.AddCookie(cookie)
		return s.guestFor(nil, r, false)
	}
	if !s.GuestNeedsCheck(again()) {
		t.Error("still unverified after a bad token")
	}
	if call("good") != 200 || s.GuestNeedsCheck(again()) {
		t.Error("a good token verifies the visitor for the rest of the trial")
	}
}

func TestOnlyAKeyTheProviderAcceptsCanTurnItOn(t *testing.T) {
	fakeProvider(t)
	e := newAdminEnv(t)
	save := func(body string) (int, string) { return e.do("POST", "/api/admin/settings/captcha", body, true) }
	if code, b := save(`{"enabled":true,"provider":"turnstile","siteKey":"s","secretKey":"badsecret","onLogin":true}`); code != 400 || !strings.Contains(b, "rejected") {
		t.Errorf("a wrong secret must be refused: %d %s", code, b)
	}
	if code, _ := save(`{"enabled":true,"provider":"turnstile","siteKey":"","secretKey":"x","onLogin":true}`); code != 400 {
		t.Error("a missing site key must be refused")
	}
	if code, _ := save(`{"enabled":true,"provider":"turnstile","siteKey":"s","secretKey":"x"}`); code != 400 {
		t.Error("it must protect somewhere")
	}
	if code, _ := save(`{"enabled":true,"provider":"hcaptcha","siteKey":"s","secretKey":"x","onLogin":true}`); code != 400 {
		t.Error("an unknown provider must be refused")
	}
	if e.svc.CaptchaRequired(ScopeLogin) {
		t.Fatal("nothing may be switched on by a refused save")
	}
	if code, b := save(`{"enabled":true,"provider":"turnstile","siteKey":"site","secretKey":"goodsecret","onLogin":true,"onRegister":true}`); code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	if !e.svc.CaptchaRequired(ScopeLogin) || e.svc.CaptchaRequired(ScopeGuest) {
		t.Error("saved scopes apply")
	}
	// The secret is stored sealed and never returned.
	_, got := e.do("GET", "/api/admin/settings", "", true)
	if strings.Contains(got, "goodsecret") || !strings.Contains(got, `"secretSet":true`) {
		t.Errorf("settings must not return the secret: %s", got)
	}
	rows, _ := e.db.Query(`SELECT value FROM settings WHERE skey = 'captcha'`)
	for rows.Next() {
		var v string
		rows.Scan(&v)
		if strings.Contains(v, "goodsecret") {
			t.Fatal("the secret is stored in clear text")
		}
	}
	rows.Close()
	// Checking a key as typed saves nothing; blank keeps the saved one.
	if code, _ := e.do("POST", "/api/admin/settings/captcha/verify", `{"provider":"turnstile","secretKey":"badsecret"}`, true); code != 400 {
		t.Error("a bad key must fail the check")
	}
	if code, _ := e.do("POST", "/api/admin/settings/captcha/verify", `{"provider":"turnstile"}`, true); code != 200 {
		t.Error("a blank key checks the saved one")
	}
	// Widget sources are allowed only while a check is on.
	script, frame, style, _, _ := e.svc.CaptchaCSP()
	if len(script) == 0 || len(frame) == 0 || len(style) != 0 {
		t.Errorf("turnstile csp: %v %v %v", script, frame, style)
	}
	e.do("POST", "/api/admin/settings/captcha", `{"enabled":false,"provider":"turnstile","siteKey":"site"}`, true)
	if s2, f2, _, _, _ := e.svc.CaptchaCSP(); len(s2)+len(f2) != 0 {
		t.Error("with the check off the page must stay locked to itself")
	}
	_ = url.Values{}
}
