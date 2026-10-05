package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

var codeRe = regexp.MustCompile(`\b(\d{3}) (\d{3})\b`)

func otpSvc(t *testing.T) (*Service, *inbox) {
	in := newInbox(t)
	s := newSvc(t, Config{Registration: RegOpen, OTPLogin: true, SMTP: in.config(), PublicURL: "https://b.example.test"})
	if _, err := s.CreateUser("ada@x.test", "Ada", goodPW, RoleUser); err != nil {
		t.Fatal(err)
	}
	return s, in
}

func codeFrom(t *testing.T, in *inbox, n int) string {
	t.Helper()
	subject, text, html := parts(t, in.wait(t, n))
	m := codeRe.FindStringSubmatch(text)
	if m == nil || !strings.Contains(subject, m[1]+m[2]) || !strings.Contains(html, "#e8663a") {
		t.Fatalf("the email must carry the code, in BLASTA's look: %q %q", subject, text)
	}
	return m[1] + m[2]
}

func TestSignInWithAnEmailedCode(t *testing.T) {
	s, in := otpSvc(t)
	if err := s.RequestOTP("Ada@X.test", "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	code := codeFrom(t, in, 1)
	u, tok, err := s.VerifyOTP("ada@x.test", code[:3]+" "+code[3:], "1.1.1.1") // spaces are fine
	if err != nil || u.Email != "ada@x.test" {
		t.Fatalf("%v", err)
	}
	if _, err := s.Authenticate(tok); err != nil {
		t.Errorf("the session must work: %v", err)
	}
	if _, _, err := s.VerifyOTP("ada@x.test", code, "1.1.1.1"); err != ErrBadCode {
		t.Errorf("a code works once, got %v", err)
	}
}

func TestWrongCodesAreLimited(t *testing.T) {
	s, in := otpSvc(t)
	s.RequestOTP("ada@x.test", "1.1.1.1")
	good := codeFrom(t, in, 1)
	wrong := "000000"
	if good == wrong {
		wrong = "000001"
	}
	for i := 0; i < otpMaxAttempts; i++ {
		if _, _, err := s.VerifyOTP("ada@x.test", wrong, "1.1.1.1"); err == nil {
			t.Fatal("a wrong code must fail")
		}
	}
	if _, _, err := s.VerifyOTP("ada@x.test", good, "1.1.1.1"); err == nil {
		t.Error("after too many wrong guesses even the right code must be dead")
	}
}

func TestCodesExpireAndAskingIsPrivateAndLimited(t *testing.T) {
	s, in := otpSvc(t)
	now := s.now()
	s.RequestOTP("ada@x.test", "1.1.1.1")
	code := codeFrom(t, in, 1)
	s.now = func() time.Time { return now.Add(11 * time.Minute) }
	if _, _, err := s.VerifyOTP("ada@x.test", code, "1.1.1.1"); err != ErrBadCode {
		t.Errorf("an expired code: %v", err)
	}
	s.now = func() time.Time { return now }
	// Unknown and disabled addresses look exactly like known ones, and send nothing.
	n := in.count()
	if err := s.RequestOTP("ghost@x.test", "2.2.2.2"); err != nil {
		t.Errorf("an unknown address must look normal: %v", err)
	}
	u := s.store.UserByEmail("ada@x.test")
	s.store.UpdateUser(u.ID, func(x *User) { x.Status = StatusDisabled })
	s.RequestOTP("ada@x.test", "3.3.3.3")
	time.Sleep(150 * time.Millisecond)
	if in.count() != n {
		t.Error("no code may be sent to an unknown or disabled account")
	}
	// Asking over and over is throttled.
	var th *ThrottledError
	var err error
	for i := 0; i < 8; i++ {
		err = s.RequestOTP("busy@x.test", "4.4.4.4")
	}
	if !errors.As(err, &th) {
		t.Errorf("repeated requests must be throttled: %v", err)
	}
}

func TestEmailedCodeConfirmsAnUnverifiedAddress(t *testing.T) {
	in := newInbox(t)
	s := newSvc(t, Config{Registration: RegOpen, OTPLogin: true, SMTP: in.config(), PublicURL: "https://b.example.test"})
	s.CreateUser("admin@x.test", "A", goodPW, RoleAdmin)
	u, _ := s.Register("new@x.test", "Nia", goodPW, "", "1.1.1.1")
	if u.Status != StatusUnverified {
		t.Fatalf("setup: %s", u.Status)
	}
	in.wait(t, 1) // the confirmation email
	s.RequestOTP("new@x.test", "1.1.1.2")
	code := codeFrom(t, in, 2)
	got, _, err := s.VerifyOTP("new@x.test", code, "1.1.1.2")
	if err != nil || got.Status != StatusActive {
		t.Errorf("a valid code proves the address: %+v %v", got, err)
	}
}

func TestOTPAndResetFollowTheSettings(t *testing.T) {
	// Off by default, and needs email.
	if s := newSvc(t, Config{}); s.OTPAvailable() || s.PublicConfig().OTP {
		t.Error("code sign-in is opt-in")
	}
	in := newInbox(t)
	if s := newSvc(t, Config{OTPLogin: true}); s.OTPAvailable() {
		t.Error("code sign-in needs email")
	}
	s := newSvc(t, Config{SMTP: in.config(), PublicURL: "https://b.example.test"})
	if !s.PublicConfig().Reset || s.PublicConfig().OTP {
		t.Errorf("reset is on by default, codes are off: %+v", s.PublicConfig())
	}
	off := newSvc(t, Config{SMTP: in.config(), DisableReset: true, PublicURL: "https://b.example.test"})
	if off.PublicConfig().Reset {
		t.Error("an administrator can turn reset off")
	}
	if err := off.RequestReset("a@b.test", "1.1.1.1"); err != ErrResetOff {
		t.Errorf("reset when off: %v", err)
	}
	// The admin switch refuses codes without email.
	e := newAdminEnv(t)
	if code, _ := e.do("POST", "/api/admin/settings/general", `{"registration":"open","otpLogin":true}`, true); code != 400 {
		t.Error("codes cannot be switched on without email")
	}
	_ = httptest.NewRecorder
	_ = http.StatusOK
}

// A reset request for an account with no password (made through single sign-on) still
// gets an email, saying how it works, and never a link that would create a password.
func TestResetForAnSSOAccountExplainsInsteadOfStayingSilent(t *testing.T) {
	in := newInbox(t)
	s := newSvc(t, Config{SMTP: in.config(), PublicURL: "https://b.example.test"})
	u, _ := s.CreateUser("sso@x.test", "Sam", goodPW, RoleUser)
	s.store.UpdateUser(u.ID, func(x *User) { x.PasswordHash = "" })
	if err := s.RequestResetFrom("https://b.example.test", "sso@x.test", "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	subject, text, html := parts(t, in.wait(t, 1))
	if subject != "About your BLASTA password" || !strings.Contains(text, "no password") || !strings.Contains(text, "My account") {
		t.Errorf("explanatory email: %q %q", subject, text)
	}
	if strings.Contains(text, "token=") || strings.Contains(html, "token=") {
		t.Error("no link that creates a password may be sent")
	}
	if _, ok := s.store.TakeResetToken("", s.now()); ok {
		t.Error("no reset token for a passwordless account")
	}
}

// Failed deliveries are remembered for the administrator, with the address masked,
// and a later success clears the note.
func TestFailedDeliveryIsRecordedForTheAdministrator(t *testing.T) {
	s := newSvc(t, Config{SMTP: mailerCfgUnreachable(), PublicURL: "https://b.example.test"})
	s.CreateUser("ada@x.test", "Ada", goodPW, RoleUser)
	s.RequestResetFrom("https://b.example.test", "ada@x.test", "1.1.1.1")
	var p *MailProblem
	for i := 0; i < 100 && p == nil; i++ {
		time.Sleep(30 * time.Millisecond)
		p = s.LastMailProblem()
	}
	if p == nil || !strings.Contains(p.Error, "cannot reach") || p.To != "a***@x.test" || p.Subject == "" {
		t.Fatalf("the failure must be recorded, masked: %+v", p)
	}
	in := newInbox(t)
	s.base.SMTP = in.config()
	s.Reload()
	s.RequestResetFrom("https://b.example.test", "ada@x.test", "1.1.1.2")
	in.wait(t, 1)
	for i := 0; i < 50 && s.LastMailProblem() != nil; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if s.LastMailProblem() != nil {
		t.Error("a successful delivery must clear the note")
	}
}
