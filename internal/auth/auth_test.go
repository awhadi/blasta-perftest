package auth

import (
	"errors"
	"github.com/awhadi/blasta-perftest/internal/db"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	pbkdf2Iterations = 1000 // fast tests; production uses 600k
	dummyHash, _ = HashPassword("blasta-timing-equaliser")
	os.Exit(m.Run())
}

func newSvc(t *testing.T, cfg Config) *Service {
	t.Helper()
	d, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	s, err := New(NewStore(d), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

const goodPW = "correct horse battery"

func TestPasswordHashing(t *testing.T) {
	h, err := HashPassword(goodPW)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "pbkdf2-sha256$") || strings.Contains(h, goodPW) {
		t.Errorf("unexpected hash format: %s", h)
	}
	h2, _ := HashPassword(goodPW)
	if h == h2 {
		t.Error("two hashes of one password must differ (random salt)")
	}
	if !VerifyPassword(goodPW, h) || VerifyPassword(goodPW+"x", h) || VerifyPassword("", h) {
		t.Error("verification wrong")
	}
	for _, bad := range []string{"", "plain", "pbkdf2-sha256$1$a$b", "pbkdf2-sha256$abc$AAAA$AAAA", "md5$1000$AAAA$AAAA"} {
		if VerifyPassword(goodPW, bad) {
			t.Errorf("malformed hash %q must never verify", bad)
		}
	}
	if VerifyPassword(strings.Repeat("a", maxPasswordBytes+1), h) {
		t.Error("oversized password must be refused")
	}
}

func TestPasswordPolicy(t *testing.T) {
	for _, tc := range []struct {
		pw, email string
		ok        bool
	}{
		{goodPW, "a@b.test", true},
		{"short", "a@b.test", false},
		{"aaaaaaaaaaaa", "a@b.test", false},
		{"my-alice-secret-1", "alice@b.test", false}, // contains the email name
		{"password1234", "a@b.test", false},
		{"a-long-passphrase-with-password-inside", "a@b.test", true}, // long passphrases are fine
	} {
		if err := CheckPassword(tc.pw, tc.email); (err == nil) != tc.ok {
			t.Errorf("CheckPassword(%q, %q) = %v, want ok=%v", tc.pw, tc.email, err, tc.ok)
		}
	}
}

func TestFirstAccountIsAdminThenApprovalFlow(t *testing.T) {
	s := newSvc(t, Config{Registration: RegApproval})
	admin, err := s.Register("Admin@Example.test", "Ada", goodPW, "", "1.1.1.1")
	if err != nil || admin.Role != RoleAdmin || admin.Status != StatusActive || admin.Email != "admin@example.test" {
		t.Fatalf("first account must be an active admin with a normalised email: %+v err=%v", admin, err)
	}
	u, err := s.Register("bob@example.test", "Bob", goodPW, "", "1.1.1.2")
	if err != nil || u.Role != RoleUser || u.Status != StatusPending {
		t.Fatalf("later accounts wait for approval: %+v err=%v", u, err)
	}
	if _, _, err := s.Login("bob@example.test", goodPW, "2.2.2.2"); !errors.Is(err, ErrPending) {
		t.Errorf("a pending account must not sign in, got %v", err)
	}
	if _, _, err := s.Login("bob@example.test", "wrong password!", "2.2.2.2"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("a wrong password must not reveal the account is pending, got %v", err)
	}
	if _, err := s.SetStatus(u.ID, StatusActive); err != nil {
		t.Fatal(err)
	}
	got, token, err := s.Login("bob@example.test", goodPW, "2.2.2.2")
	if err != nil || got.Email != "bob@example.test" || token == "" {
		t.Fatalf("approved account must sign in: %v", err)
	}
	if who, err := s.Authenticate(token); err != nil || who.ID != u.ID {
		t.Errorf("session did not authenticate: %v", err)
	}
	// Disabling signs the person out everywhere, at once.
	if _, err := s.SetStatus(u.ID, StatusDisabled); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(token); err == nil {
		t.Error("a disabled user's existing session must stop working")
	}
	if _, _, err := s.Login("bob@example.test", goodPW, "2.2.2.2"); !errors.Is(err, ErrDisabled) {
		t.Errorf("a disabled account must not sign in, got %v", err)
	}
}

func TestRegistrationModes(t *testing.T) {
	closed := newSvc(t, Config{Registration: RegClosed})
	if _, err := closed.Register("first@x.test", "", goodPW, "", "9.9.9.9"); err != nil {
		t.Fatalf("the very first account must always be possible: %v", err)
	}
	if _, err := closed.Register("second@x.test", "", goodPW, "", "9.9.9.8"); !errors.Is(err, ErrRegistrationClosed) {
		t.Errorf("closed registration must refuse, got %v", err)
	}
	open := newSvc(t, Config{Registration: RegOpen})
	open.Register("first@x.test", "", goodPW, "", "9.9.9.1")
	if u, err := open.Register("second@x.test", "", goodPW, "", "9.9.9.2"); err != nil || u.Status != StatusActive {
		t.Errorf("open registration gives an active account: %+v %v", u, err)
	}
	if _, err := New(&Store{}, Config{Registration: "wide-open"}); err == nil {
		t.Error("an unknown registration mode must be rejected at start-up")
	}
}

func TestDomainRestrictionAndSetupToken(t *testing.T) {
	s := newSvc(t, Config{AllowedDomains: []string{"@Acme.test"}, SetupToken: "s3cret"})
	if _, err := s.Register("a@evil.test", "", goodPW, "s3cret", "1.1.1.1"); !errors.Is(err, ErrDomain) {
		t.Errorf("a disallowed domain must be refused, got %v", err)
	}
	if _, err := s.Register("a@acme.test", "", goodPW, "wrong", "1.1.1.2"); !errors.Is(err, ErrSetupToken) {
		t.Errorf("the first account needs the setup token, got %v", err)
	}
	if _, err := s.Register("a@acme.test", "", goodPW, "", "1.1.1.3"); !errors.Is(err, ErrSetupToken) {
		t.Errorf("an empty setup token must not match, got %v", err)
	}
	if u, err := s.Register("a@acme.test", "", goodPW, "s3cret", "1.1.1.4"); err != nil || u.Role != RoleAdmin {
		t.Errorf("the right token creates the admin: %+v %v", u, err)
	}
	if pc := s.PublicConfig(); pc.NeedsSetup || pc.NeedsSetupToken {
		t.Errorf("setup is finished: %+v", pc)
	}
}

func TestBadInput(t *testing.T) {
	s := newSvc(t, Config{})
	for _, email := range []string{"", "nope", "a@", "@b.test", "a b@c.test", "Name <a@b.test>"} {
		if _, err := s.Register(email, "", goodPW, "", "3.3.3.3"); !errors.Is(err, ErrBadEmail) {
			t.Errorf("email %q must be refused, got %v", email, err)
		}
	}
	var weak WeakPasswordError
	if _, err := s.Register("a@b.test", "", "short", "", "3.3.3.4"); !errors.As(err, &weak) {
		t.Errorf("a weak password must be refused with its reason, got %v", err)
	}
	if _, err := s.Register("a@b.test", "", goodPW, "", "3.3.3.5"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Register("A@B.test", "", goodPW, "", "3.3.3.6"); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("a duplicate email (any case) must be refused, got %v", err)
	}
}

func TestLoginThrottle(t *testing.T) {
	s := newSvc(t, Config{})
	s.Register("a@b.test", "", goodPW, "", "7.7.7.7")
	for i := 0; i < 5; i++ {
		if _, _, err := s.Login("a@b.test", "wrong password", "8.8.8.8"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	var th *ThrottledError
	if _, _, err := s.Login("a@b.test", goodPW, "8.8.8.8"); !errors.As(err, &th) {
		t.Fatalf("after 5 failures even the right password is refused until the window passes, got %v", err)
	}
	// Another address cannot lock the real owner out of... but the email key is shared:
	if _, _, err := s.Login("a@b.test", goodPW, "4.4.4.4"); !errors.As(err, &th) {
		t.Errorf("the lockout follows the account, not only the address, got %v", err)
	}
	// Time passes: the lockout lifts and a success clears the counter.
	s.now = func() time.Time { return time.Now().Add(16 * time.Minute) }
	if _, _, err := s.Login("a@b.test", goodPW, "8.8.8.8"); err != nil {
		t.Errorf("the lockout must lift after the window: %v", err)
	}
	// Unknown emails are throttled too (no way to probe for accounts quickly).
	for i := 0; i < 5; i++ {
		s.Login("ghost@b.test", "x", "5.5.5.5")
	}
	if _, _, err := s.Login("ghost@b.test", "x", "5.5.5.5"); !errors.As(err, &th) {
		t.Errorf("unknown emails must be throttled as well, got %v", err)
	}
}

func TestSessionsExpire(t *testing.T) {
	s := newSvc(t, Config{SessionIdle: time.Hour, SessionMax: 3 * time.Hour})
	s.Register("a@b.test", "", goodPW, "", "7.7.7.7")
	_, token, _ := s.Login("a@b.test", goodPW, "7.7.7.7")
	base := time.Now()
	s.now = func() time.Time { return base.Add(30 * time.Minute) }
	if _, err := s.Authenticate(token); err != nil {
		t.Fatalf("an active session must work: %v", err)
	}
	s.now = func() time.Time { return base.Add(100 * time.Minute) } // 70 min idle
	if _, err := s.Authenticate(token); err == nil {
		t.Error("an idle session must expire")
	}
	_, token, _ = s.Login("a@b.test", goodPW, "7.7.7.7")
	for i := 1; i <= 4; i++ { // stays active, but passes the absolute limit
		s.now = func() time.Time { return base.Add(time.Duration(100+i*55) * time.Minute) }
		s.Authenticate(token)
	}
	s.now = func() time.Time { return base.Add(100*time.Minute + 4*time.Hour) }
	if _, err := s.Authenticate(token); err == nil {
		t.Error("a session must end at the absolute limit even if it is kept busy")
	}
	if _, err := s.Authenticate("not-a-token"); err == nil {
		t.Error("a made-up token must not authenticate")
	}
}

func TestLastAdminIsProtected(t *testing.T) {
	s := newSvc(t, Config{})
	a, _ := s.Register("a@b.test", "", goodPW, "", "1.0.0.1")
	if _, err := s.SetRole(a.ID, RoleUser); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("demoting the only admin must be refused, got %v", err)
	}
	if _, err := s.SetStatus(a.ID, StatusDisabled); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("disabling the only admin must be refused, got %v", err)
	}
	if err := s.Delete(a.ID); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("deleting the only admin must be refused, got %v", err)
	}
	b, _ := s.CreateUser("b@b.test", "", goodPW, RoleAdmin)
	if _, err := s.SetRole(a.ID, RoleUser); err != nil {
		t.Errorf("with a second admin the first can be demoted: %v", err)
	}
	if err := s.Delete(b.ID); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("but the remaining admin is protected: %v", err)
	}
}

func TestPasswordChangeEndsSessions(t *testing.T) {
	s := newSvc(t, Config{})
	u, _ := s.Register("a@b.test", "", goodPW, "", "1.0.0.1")
	_, token, _ := s.Login("a@b.test", goodPW, "1.0.0.1")
	if err := s.SetPassword(u.ID, "a brand new passphrase"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(token); err == nil {
		t.Error("changing the password must sign the user out everywhere")
	}
	if _, _, err := s.Login("a@b.test", goodPW, "1.0.0.1"); err == nil {
		t.Error("the old password must stop working")
	}
	if _, _, err := s.Login("a@b.test", "a brand new passphrase", "1.0.0.1"); err != nil {
		t.Errorf("the new password must work: %v", err)
	}
}

func TestStorePersistsAndKeepsSecretsPrivate(t *testing.T) {
	dir := t.TempDir()
	d, err := db.Open("", dir)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := New(NewStore(d), Config{})
	u, _ := s.Register("a@b.test", "Ada", goodPW, "", "1.0.0.1")
	_, token, _ := s.Login("a@b.test", goodPW, "1.0.0.1")

	info, err := os.Stat(filepath.Join(dir, "blasta.db"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the database file must be owner-only (0600): %v %v", info, err)
	}
	d.Close() // flush the WAL so the file holds everything
	raw, _ := os.ReadFile(filepath.Join(dir, "blasta.db"))
	if strings.Contains(string(raw), token) || strings.Contains(string(raw), goodPW) {
		t.Fatal("neither a session token nor a password may be stored in clear text")
	}

	d2, err := db.Open("", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	s2, _ := New(NewStore(d2), Config{})
	if who, err := s2.Authenticate(token); err != nil || who.ID != u.ID {
		t.Errorf("a session must survive a restart: %v", err)
	}
	if _, _, err := s2.Login("a@b.test", goodPW, "1.0.0.1"); err != nil {
		t.Errorf("the account must survive a restart: %v", err)
	}
}

func TestThrottleUnit(t *testing.T) {
	th := NewThrottle(2, time.Minute)
	now := time.Now()
	th.Fail("k", now)
	if locked, _ := th.Locked("k", now); locked {
		t.Error("one failure is not a lockout")
	}
	th.Fail("k", now)
	if locked, wait := th.Locked("k", now); !locked || wait <= 0 {
		t.Errorf("two failures lock: %v %v", locked, wait)
	}
	if locked, _ := th.Locked("k", now.Add(2*time.Minute)); locked {
		t.Error("the lock lifts after the window")
	}
	th.Fail("j", now)
	th.Fail("j", now)
	th.Reset("j")
	if locked, _ := th.Locked("j", now); locked {
		t.Error("a reset clears the key")
	}
}

// The CLI edits the same database while the server runs: each side must see the
// other's changes at once and never overwrite them.
func TestStoreSeesChangesMadeByAnotherProcess(t *testing.T) {
	dir := t.TempDir()
	d1, err := db.Open("", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d1.Close()
	d2, err := db.Open("", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	server, cli := NewStore(d1), NewStore(d2)
	srv, _ := New(server, Config{})
	cliSvc, _ := New(cli, Config{})
	if _, err := srv.CreateUser("admin@x.test", "", goodPW, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	u := cli.UserByEmail("admin@x.test")
	if err := cliSvc.SetPassword(u.ID, "a freshly reset passphrase"); err != nil {
		t.Fatal(err)
	}
	if _, err := cliSvc.CreateUser("late@x.test", "", goodPW, RoleUser); err != nil {
		t.Fatal(err)
	}
	if _, _, err := srv.Login("admin@x.test", "a freshly reset passphrase", "1.1.1.1"); err != nil {
		t.Errorf("the running server must accept the password the CLI just set: %v", err)
	}
	if server.UserByEmail("late@x.test") == nil {
		t.Error("the running server must see a user the CLI just created")
	}
	if _, err := srv.CreateUser("after@x.test", "", goodPW, RoleUser); err != nil {
		t.Fatal(err)
	}
	if cli.UserByEmail("late@x.test") == nil || cli.UserByEmail("after@x.test") == nil {
		t.Error("neither side's changes may be lost")
	}
}
