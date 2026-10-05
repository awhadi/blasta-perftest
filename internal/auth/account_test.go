package auth

import (
	"encoding/base64"
	"github.com/awhadi/blasta-perftest/internal/mailer"
	"strings"
	"testing"
)

func TestProfileName(t *testing.T) {
	s := newSvc(t, Config{})
	u, _ := s.CreateUser("a@b.test", "Ada", goodPW, RoleUser)
	if got, err := s.UpdateProfile(u.ID, "  Ada Lovelace  "); err != nil || got.Name != "Ada Lovelace" {
		t.Errorf("%+v %v", got, err)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("x", 81)} {
		if _, err := s.UpdateProfile(u.ID, bad); err != ErrBadName {
			t.Errorf("%q must be refused, got %v", bad, err)
		}
	}
}

func TestEmailChangeWithoutMailAppliesAtOnce(t *testing.T) {
	s := newSvc(t, Config{})
	u, _ := s.CreateUser("old@b.test", "Ada", goodPW, RoleUser)
	s.CreateUser("taken@b.test", "T", goodPW, RoleUser)
	if _, err := s.ChangeEmail("", u, "new@b.test", "wrong password!"); err != ErrInvalidCredentials {
		t.Errorf("the current password is needed: %v", err)
	}
	if _, err := s.ChangeEmail("", u, "old@b.test", goodPW); err != ErrSameEmail {
		t.Errorf("same address: %v", err)
	}
	if _, err := s.ChangeEmail("", u, "taken@b.test", goodPW); err != ErrEmailTaken {
		t.Errorf("taken address: %v", err)
	}
	if _, err := s.ChangeEmail("", u, "not-an-email", goodPW); err != ErrBadEmail {
		t.Errorf("bad address: %v", err)
	}
	if applied, err := s.ChangeEmail("", u, "New@B.test", goodPW); err != nil || !applied {
		t.Fatalf("%v %v", applied, err)
	}
	if s.store.UserByEmail("new@b.test") == nil || s.store.UserByEmail("old@b.test") != nil {
		t.Error("the address must have moved")
	}
	if _, _, err := s.Login("new@b.test", goodPW, "1.1.1.1"); err != nil {
		t.Errorf("signing in with the new address: %v", err)
	}
}

func TestEmailChangeWithMailNeedsConfirmationFromTheNewAddress(t *testing.T) {
	in := newInbox(t)
	s := newSvc(t, Config{SMTP: in.config(), PublicURL: "https://b.example.test"})
	u, _ := s.CreateUser("old@b.test", "Ada", goodPW, RoleUser)
	applied, err := s.ChangeEmail("https://b.example.test", u, "new@b.test", goodPW)
	if err != nil || applied {
		t.Fatalf("with email the change waits for confirmation: %v %v", applied, err)
	}
	if s.store.UserByEmail("old@b.test") == nil {
		t.Fatal("nothing may change before the new address is confirmed")
	}
	subject, text, html := parts(t, in.wait(t, 1))
	if !strings.Contains(subject, "Confirm your new email") || !strings.Contains(html, "#e8663a") {
		t.Errorf("email: %q", subject)
	}
	i := strings.Index(text, "#/confirm-email?token=")
	if i < 0 {
		t.Fatalf("no link in %q", text)
	}
	token := strings.Fields(text[i+len("#/confirm-email?token="):])[0]
	got, err := s.ConfirmEmailChange(token)
	if err != nil || got.Email != "new@b.test" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := s.ConfirmEmailChange(token); err == nil {
		t.Error("the link works once")
	}
	// SSO-only accounts do not change email here.
	sso, _ := s.store.UpdateUser(u.ID, func(x *User) { x.PasswordHash = "" })
	if _, err := s.ChangeEmail("", sso, "x@b.test", goodPW); err != ErrSSOEmail {
		t.Errorf("sso: %v", err)
	}
}

func TestAvatarAcceptsOnlySmallRealImages(t *testing.T) {
	s := newSvc(t, Config{})
	u, _ := s.CreateUser("a@b.test", "Ada", goodPW, RoleUser)
	jpeg := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte{0xFF, 0xD8, 0xFF, 0xE0, 1, 2, 3})
	if got, err := s.SetAvatar(u.ID, jpeg); err != nil || got.Avatar != jpeg {
		t.Fatalf("%v", err)
	}
	if s.store.UserByID(u.ID).Avatar != jpeg {
		t.Error("the picture must be stored")
	}
	for name, bad := range map[string]string{
		"svg":          "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte("<svg onload=alert(1)>")),
		"wrong bytes":  "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("<html>not a png</html>")),
		"not base64":   "data:image/jpeg;base64,@@@@",
		"a script url": "javascript:alert(1)",
		"remote":       "https://evil.test/x.png",
	} {
		if _, err := s.SetAvatar(u.ID, bad); err != ErrBadImage {
			t.Errorf("%s must be refused, got %v", name, err)
		}
	}
	if _, err := s.SetAvatar(u.ID, "data:image/jpeg;base64,"+base64.StdEncoding.EncodeToString(append([]byte{0xFF, 0xD8, 0xFF}, make([]byte, 200_000)...))); err != ErrImageLarge {
		t.Errorf("a large picture: %v", err)
	}
	if got, err := s.SetAvatar(u.ID, ""); err != nil || got.Avatar != "" {
		t.Error("an empty picture removes it")
	}
}

// An account made through single sign-on has no password: nothing signs it in with
// one, until its owner sets one from their account page.
func TestSSOAccountsHaveNoPasswordUntilTheyChooseOne(t *testing.T) {
	e := newAdminEnv(t)
	sso, err := e.svc.store.UpdateUser(e.svc.store.UserByEmail("admin@x.test").ID, func(x *User) { x.PasswordHash = ""; x.Identities = []Identity{{Provider: "oidc", Subject: "s1"}} })
	if err != nil || sso.PasswordHash != "" {
		t.Fatal(err)
	}
	for _, pw := range []string{"", goodPW, "anything at all", dummyHash} {
		if _, _, err := e.svc.Login("admin@x.test", pw, "7.7.7.7"); err == nil {
			t.Fatalf("a passwordless account must never sign in with a password (%q)", pw)
		}
	}
	// A reset email must not create a first password either.
	in := newInbox(t)
	e.svc.base.SMTP = in.config()
	e.svc.base.PublicURL = "https://b.example.test"
	e.svc.Reload()
	e.svc.RequestResetFrom("https://b.example.test", "admin@x.test", "7.7.7.7")
	_, text, _ := parts(t, in.wait(t, 1)) // an explanation arrives, but never a link that sets a password
	if strings.Contains(text, "token=") {
		t.Error("no reset link may be sent to an account without a password")
	}

	set := func(body string) (int, string) { return e.do("POST", "/api/auth/password", body, true) }
	if code, _ := set(`{"new":"short"}`); code != 400 {
		t.Errorf("a weak first password must be refused: %d", code)
	}
	// (the session cookie is replaced when a password is set, so use a fresh one)
	_, tok, err := func() (*User, string, error) { return e.svc.startSession(sso) }()
	if err != nil {
		t.Fatal(err)
	}
	e.token = tok
	if code, b := set(`{"new":"a first password of mine"}`); code != 200 || !strings.Contains(b, `"hadPassword":"false"`) {
		t.Fatalf("setting a first password needs no current one: %d %s", code, b)
	}
	_, tok2, err := e.svc.Login("admin@x.test", "a first password of mine", "7.7.7.8")
	if err != nil {
		t.Fatalf("after setting one, password sign-in works: %v", err)
	}
	if _, err := e.svc.Authenticate(tok); err == nil {
		t.Error("setting a password signs the older sessions out")
	}
	e.token = tok2
	// From now on changing it does need the current one.
	if code, _ := set(`{"new":"another good passphrase","current":"wrong one"}`); code != 403 {
		t.Errorf("changing a password needs the current one: %d", code)
	}
	if u := e.svc.store.UserByEmail("admin@x.test"); len(u.Identities) != 1 {
		t.Error("setting a password must keep the SSO link")
	}
}

func mailerCfgUnreachable() mailer.Config {
	return mailer.Config{Host: "127.0.0.1", Port: 1, Security: "none", From: "BLASTA@x.test"}
}
