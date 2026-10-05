package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/awhadi/blasta-perftest/internal/db"
	"github.com/awhadi/blasta-perftest/internal/settings"
)

// adminEnv is a service with settings, served over HTTP, and a signed-in admin.
type adminEnv struct {
	t     *testing.T
	svc   *Service
	srv   *httptest.Server
	token string
	db    *db.DB
}

func newAdminEnv(t *testing.T) *adminEnv {
	t.Helper()
	svc := newSvc(t, Config{Registration: RegApproval})
	box, err := settings.NewBox([]byte("0123456789abcdef-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.UseSettings(settings.New(svc.store.DB(), box)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateUser("admin@x.test", "Admin", goodPW, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	_, token, err := svc.Login("admin@x.test", goodPW, "9.9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	svc.Routes(mux)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r2, ok := svc.Gate(w, r)
		if ok {
			mux.ServeHTTP(w, r2)
		}
	}))
	t.Cleanup(srv.Close)
	return &adminEnv{t: t, svc: svc, srv: srv, token: token, db: svc.store.DB()}
}

func (e *adminEnv) do(method, path, body string, asAdmin bool) (int, string) {
	req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if asAdmin {
		req.AddCookie(&http.Cookie{Name: SessionCookie, Value: e.token})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, b.String()
}

func TestSettingsAreAdminOnly(t *testing.T) {
	e := newAdminEnv(t)
	if code, _ := e.do("GET", "/api/admin/settings", "", false); code != 401 {
		t.Errorf("anonymous = %d, want 401", code)
	}
	if _, err := e.svc.CreateUser("user@x.test", "U", goodPW, RoleUser); err != nil {
		t.Fatal(err)
	}
	_, tok, _ := e.svc.Login("user@x.test", goodPW, "9.9.9.8")
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/admin/settings", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: tok})
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 403 {
		t.Errorf("a normal user = %d, want 403", resp.StatusCode)
	}
	if code, _ := e.do("GET", "/api/admin/settings", "", true); code != 200 {
		t.Errorf("admin = %d", code)
	}
}

// SSO and mail secrets are encrypted in the database and never sent to the browser.
func TestSecretsAreSealedAndNeverReturned(t *testing.T) {
	e := newAdminEnv(t)
	code, b := e.do("POST", "/api/admin/settings/smtp",
		`{"enabled":true,"host":"smtp.x.test","port":587,"security":"starttls","username":"u","password":"smtp-secret-pw","from":"BLASTA@x.test"}`, true)
	if code != 200 {
		t.Fatalf("save smtp: %d %s", code, b)
	}
	code, b = e.do("POST", "/api/admin/settings/sso",
		`{"enabled":true,"name":"Acme","issuer":"https://idp.x.test","clientId":"cid","clientSecret":"oidc-secret-value","autoCreate":true}`, true)
	if code != 200 {
		t.Fatalf("save sso: %d %s", code, b)
	}
	_, got := e.do("GET", "/api/admin/settings", "", true)
	for _, secret := range []string{"smtp-secret-pw", "oidc-secret-value"} {
		if strings.Contains(got, secret) {
			t.Errorf("the settings page must never receive %q", secret)
		}
	}
	if !strings.Contains(got, `"passwordSet":true`) || !strings.Contains(got, `"clientSecretSet":true`) {
		t.Errorf("the page should learn that secrets are set: %s", got)
	}
	// In the database they are sealed, not plain.
	rows, _ := e.db.Query(`SELECT value FROM settings`)
	for rows.Next() {
		var v string
		rows.Scan(&v)
		if strings.Contains(v, "smtp-secret-pw") || strings.Contains(v, "oidc-secret-value") {
			t.Fatal("a secret is stored in clear text")
		}
	}
	rows.Close()
	// ...and the running service uses them.
	if c := e.svc.Mailer(); c.Password != "smtp-secret-pw" || c.Host != "smtp.x.test" {
		t.Errorf("mail settings not applied: %+v", c)
	}
	pc := e.svc.PublicConfig()
	if !pc.SSO || pc.SSOName != "Acme" || !pc.Email {
		t.Errorf("sign-in page config not updated live: %+v", pc)
	}
	// Saving again without a secret keeps the stored one.
	if code, b := e.do("POST", "/api/admin/settings/smtp",
		`{"enabled":true,"host":"smtp2.x.test","port":465,"security":"tls","from":"BLASTA@x.test"}`, true); code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	if c := e.svc.Mailer(); c.Password != "smtp-secret-pw" || c.Host != "smtp2.x.test" {
		t.Errorf("a blank password must keep the saved one: %+v", c)
	}
}

// A bad setting must be refused and must not break sign-in for everyone.
func TestBadSettingsAreRefusedAndRolledBack(t *testing.T) {
	e := newAdminEnv(t)
	for name, body := range map[string]string{
		"sso":     `{"enabled":true,"issuer":"http://idp.example.com","clientId":"x"}`,
		"smtp":    `{"enabled":true,"host":"h","port":99999,"security":"tls","from":"a@b.test"}`,
		"general": `{"publicUrl":"ftp://nope","registration":"approval"}`,
		"guest":   `{"enabled":true,"trialMinutes":0,"maxRuns":1,"maxDurationSec":10,"maxRps":10,"maxConcurrency":1,"dailyPerIp":1}`,
	} {
		if code, _ := e.do("POST", "/api/admin/settings/"+name, body, true); code != 400 {
			t.Errorf("%s: bad value accepted (%d)", name, code)
		}
	}
	if code, b := e.do("POST", "/api/admin/settings/general", `{"publicUrl":"https://blasta.x.test","registration":"nonsense"}`, true); code != 400 {
		t.Errorf("an unknown registration mode must be refused, got %d %s", code, b)
	}
	if e.svc.conf().Registration != RegApproval || e.svc.PublicConfig().SSO {
		t.Error("a refused change must leave the running configuration alone")
	}
	var n int
	e.db.QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&n)
	if n != 0 {
		t.Errorf("a refused change must not be saved (%d rows)", n)
	}
}

func TestGeneralSettingsApplyAndReset(t *testing.T) {
	e := newAdminEnv(t)
	if code, b := e.do("POST", "/api/admin/settings/general",
		`{"publicUrl":"https://blasta.x.test/","registration":"open","allowedDomains":["@Acme.test", " "]}`, true); code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	c := e.svc.conf()
	if c.Registration != RegOpen || c.PublicURL != "https://blasta.x.test" || len(c.AllowedDomains) != 1 || c.AllowedDomains[0] != "acme.test" {
		t.Errorf("general settings not applied: %+v", c)
	}
	if code, _ := e.do("DELETE", "/api/admin/settings/general", "", true); code != 200 {
		t.Fatal("reset failed")
	}
	if e.svc.conf().Registration != RegApproval {
		t.Error("a reset must bring back the environment's value")
	}
}

func TestSettingsSurviveARestart(t *testing.T) {
	e := newAdminEnv(t)
	e.do("POST", "/api/admin/settings/guest", `{"enabled":false,"trialMinutes":5,"maxRuns":2,"maxDurationSec":10,"maxRps":10,"maxConcurrency":5,"dailyPerIp":9}`, true)
	box, _ := settings.NewBox([]byte("0123456789abcdef-test-key"))
	s2, _ := New(NewStore(e.db), Config{})
	if err := s2.UseSettings(settings.New(e.db, box)); err != nil {
		t.Fatal(err)
	}
	if g := s2.conf().Guest; g.Enabled || g.TrialMinutes != 5 || g.DailyPerIP != 9 {
		t.Errorf("guest settings lost: %+v", g)
	}
	// A different key cannot read the sealed secrets: that must be an error, not garbage.
	e.do("POST", "/api/admin/settings/smtp", `{"enabled":true,"host":"h","port":25,"security":"none","password":"pw","from":"a@b.test"}`, true)
	other, _ := settings.NewBox([]byte("a-completely-different-key"))
	s3, _ := New(NewStore(e.db), Config{})
	if err := s3.UseSettings(settings.New(e.db, other)); err == nil {
		t.Error("settings sealed with another key must be reported, not silently dropped")
	}
}

func TestBoxRoundTrip(t *testing.T) {
	b, _ := settings.NewBox([]byte("0123456789abcdef"))
	a, _ := b.Seal("hello")
	c, _ := b.Seal("hello")
	if a == c || strings.Contains(a, "hello") {
		t.Error("each sealing must use a fresh nonce")
	}
	if got, err := b.Open(a); err != nil || got != "hello" {
		t.Errorf("%q %v", got, err)
	}
	if _, err := b.Open("v1:AAAA"); err == nil {
		t.Error("garbage must be rejected")
	}
	if _, err := settings.NewBox([]byte("short")); err == nil {
		t.Error("a short key must be refused")
	}
}

// ---- guests --------------------------------------------------------------

func TestGuestTrialLimits(t *testing.T) {
	g := settings.Guest{Enabled: true, TrialMinutes: 10, MaxRuns: 2, MaxDurationSec: 30, MaxRPS: 50, MaxConcurrency: 20, DailyPerIP: 3}
	s := newSvc(t, Config{Guest: g})
	now := time.Now()
	s.now = func() time.Time { return now }

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/jobs", nil)
	guest := s.guestFor(rec, req, true)
	if guest == nil || guest.Expired || guest.RunsUsed != 0 {
		t.Fatalf("a new visitor starts a trial: %+v", guest)
	}
	cookie := rec.Result().Cookies()[0]
	again := func() *Guest {
		r := httptest.NewRequest("GET", "/api/runs", nil)
		r.AddCookie(cookie)
		return s.guestFor(nil, r, false)
	}
	for i := 0; i < 2; i++ {
		if err := s.GuestStartRun(again(), "1.2.3.4"); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
	if err := s.GuestStartRun(again(), "1.2.3.4"); err != ErrGuestRuns {
		t.Errorf("the third run must be refused, got %v", err)
	}
	// A visitor who clears cookies still hits the per-network cap.
	other := &Guest{ID: "x", Limits: g}
	s.store.db.Exec(`INSERT INTO guests (id, first_seen, runs) VALUES ('x', ?, 0)`, now.UnixMilli())
	if err := s.GuestStartRun(other, "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if err := s.GuestStartRun(other, "1.2.3.4"); err != ErrGuestBusy {
		t.Errorf("the network cap must hold, got %v", err)
	}
	// The trial is short: after it, nothing starts.
	now = now.Add(11 * time.Minute)
	if gg := again(); !gg.Expired {
		t.Error("the trial must end after its minutes")
	} else if err := s.GuestStartRun(gg, "9.9.9.9"); err != ErrGuestExpired {
		t.Errorf("an expired trial must be refused, got %v", err)
	}
	// A day later, a fresh trial.
	now = now.Add(24 * time.Hour)
	if gg := again(); gg.Expired || gg.RunsUsed != 0 {
		t.Errorf("a day later the visitor gets a new trial: %+v", gg)
	}
}

func TestGuestsOffMeansNoGuests(t *testing.T) {
	off := settings.DefaultGuest()
	off.Enabled = false
	s := newSvc(t, Config{Guest: off})
	if g := s.guestFor(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/jobs", nil), true); g != nil {
		t.Error("guests are off")
	}
	if s.PublicConfig().Guest {
		t.Error("the page must not offer a trial")
	}
}

// ---- password reset --------------------------------------------------------

func TestPasswordResetFlow(t *testing.T) {
	s := newSvc(t, Config{PublicURL: "https://blasta.x.test"})
	if err := s.RequestReset("a@b.test", "1.1.1.1"); err != ErrMailOff {
		t.Errorf("without email, reset must say so: %v", err)
	}
	// Mail needs a server; drive the token path directly.
	u, _ := s.CreateUser("a@b.test", "A", goodPW, RoleUser)
	_, session, _ := s.Login("a@b.test", goodPW, "1.1.1.1")
	token, _ := randomToken(32)
	if err := s.store.PutResetToken(tokenHash(token), u.ID, s.now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetPassword(token, "short"); err == nil {
		t.Fatal("a weak password must be refused")
	}
	if err := s.ResetPassword(token, "a much better passphrase"); err != nil {
		t.Fatalf("the link must survive a weak attempt: %v", err)
	}
	if err := s.ResetPassword(token, "another good passphrase"); err == nil {
		t.Error("a reset link works once")
	}
	if _, err := s.Authenticate(session); err == nil {
		t.Error("a reset must sign the user out everywhere")
	}
	if _, _, err := s.Login("a@b.test", "a much better passphrase", "1.1.1.1"); err != nil {
		t.Error(err)
	}
	// Expired links do nothing.
	old, _ := randomToken(32)
	s.store.PutResetToken(tokenHash(old), u.ID, s.now().Add(-time.Minute))
	if err := s.ResetPassword(old, "yet another passphrase"); err == nil {
		t.Error("an expired link must be refused")
	}
}

func TestSettingsJSONNeverHoldsPlainSecrets(t *testing.T) {
	b, _ := json.Marshal(settings.SMTP{Password: "pw", PasswordSealed: "v1:x"})
	if strings.Contains(string(b), `"pw"`) {
		t.Error("the plain password must not be marshalled")
	}
}

func TestAdminCanAddUsers(t *testing.T) {
	e := newAdminEnv(t)
	code, b := e.do("POST", "/api/admin/users", `{"email":"new@x.test","name":"New","password":"a long enough passphrase","role":"user"}`, true)
	if code != 201 || !strings.Contains(b, `"status":"active"`) {
		t.Fatalf("%d %s", code, b)
	}
	if code, _ := e.do("POST", "/api/admin/users", `{"email":"new@x.test","password":"a long enough passphrase"}`, true); code != 409 {
		t.Errorf("a duplicate email = %d, want 409", code)
	}
	if code, _ := e.do("POST", "/api/admin/users", `{"email":"w@x.test","password":"short"}`, true); code != 400 {
		t.Errorf("a weak password = %d, want 400", code)
	}
	if code, _ := e.do("POST", "/api/admin/users", `{"email":"n2@x.test","password":"a long enough passphrase"}`, false); code != 401 {
		t.Errorf("anonymous = %d, want 401", code)
	}
	if _, _, err := e.svc.Login("new@x.test", "a long enough passphrase", "5.5.5.5"); err != nil {
		t.Errorf("the new account must be able to sign in: %v", err)
	}
}

// Removing the provider forgets its secret instead of keeping it hidden in the database.
func TestRemovingSSOClearsTheSecret(t *testing.T) {
	e := newAdminEnv(t)
	e.do("POST", "/api/admin/settings/sso", `{"enabled":true,"issuer":"https://idp.x.test","clientId":"c","clientSecret":"to-be-forgotten"}`, true)
	if code, b := e.do("POST", "/api/admin/settings/sso", `{"enabled":false,"clearSecret":true}`, true); code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	v, ok, err := e.svc.settings.GetSSO()
	if err != nil || !ok || v.ClientSecret != "" || v.SecretSealed != "" || v.Enabled {
		t.Errorf("the provider and its secret must be gone: %+v %v", v, err)
	}
	if e.svc.PublicConfig().SSO {
		t.Error("sign-in must no longer offer SSO")
	}
}

func TestSMTPCanBeCheckedBeforeSaving(t *testing.T) {
	e := newAdminEnv(t)
	in := newInbox(t)
	body := func(port int) string {
		return `{"enabled":true,"host":"127.0.0.1","port":` + strconv.Itoa(port) + `,"security":"none","from":"b@x.test"}`
	}
	code, b := e.do("POST", "/api/admin/settings/smtp/verify", body(in.port), true)
	if code != 200 || !strings.Contains(b, "Connected to 127.0.0.1") {
		t.Fatalf("%d %s", code, b)
	}
	if code, _ := e.do("POST", "/api/admin/settings/smtp/verify", body(1), true); code != 400 {
		t.Errorf("an unreachable server must be reported, got %d", code)
	}
	var n int
	e.db.QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&n)
	if n != 0 {
		t.Error("checking must not save anything")
	}
	if in.count() != 0 {
		t.Error("checking must not send a message")
	}
	if code, _ := e.do("POST", "/api/admin/settings/smtp/verify", body(in.port), false); code != 401 {
		t.Errorf("anonymous = %d", code)
	}
}
