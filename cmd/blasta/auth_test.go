package main

import (
	"github.com/awhadi/blasta-perftest/internal/db"
	"strings"
	"testing"
	"time"

	"github.com/awhadi/blasta-perftest/internal/auth"
)

func TestAuthConfigFromEnv(t *testing.T) {
	t.Setenv("BLASTA_REGISTRATION", " Open ")
	t.Setenv("BLASTA_ALLOWED_EMAIL_DOMAINS", "acme.test, other.test ,")
	t.Setenv("BLASTA_SETUP_TOKEN", "tok")
	t.Setenv("BLASTA_SESSION_IDLE", "2h")
	t.Setenv("BLASTA_OIDC_ISSUER", "https://idp.test/realms/x")
	t.Setenv("BLASTA_OIDC_CLIENT_ID", "blasta")
	t.Setenv("BLASTA_OIDC_CLIENT_SECRET", "shh")
	t.Setenv("BLASTA_OIDC_ADMIN_EMAILS", "a@acme.test,b@acme.test")
	cfg, err := authConfigFromEnv("https://blasta.test")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Registration != "open" || len(cfg.AllowedDomains) != 2 || cfg.SetupToken != "tok" ||
		cfg.SessionIdle != 2*time.Hour || cfg.PublicURL != "https://blasta.test" {
		t.Errorf("parsed wrongly: %+v", cfg)
	}
	if cfg.SSO == nil || cfg.SSO.ClientSecret != "shh" || !cfg.SSO.AutoCreate || len(cfg.SSO.AdminEmails) != 2 {
		t.Errorf("sso parsed wrongly: %+v", cfg.SSO)
	}
	t.Setenv("BLASTA_SESSION_IDLE", "soon")
	if _, err := authConfigFromEnv(""); err == nil {
		t.Error("a bad duration must be reported, not ignored")
	}
	t.Setenv("BLASTA_SESSION_IDLE", "")
	t.Setenv("BLASTA_OIDC_ISSUER", "")
	if cfg, _ := authConfigFromEnv(""); cfg.SSO != nil {
		t.Error("no issuer means no SSO")
	}
}

func TestUserCommands(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BLASTA_NEW_PASSWORD", "correct horse battery")
	if err := cmdUser([]string{"create", "--data-dir", dir, "--email", "root@acme.test", "--admin"}); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := cmdUser([]string{"create", "--data-dir", dir, "--email", "pat@acme.test"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := cmdUser([]string{"create", "--data-dir", dir, "--email", "pat@acme.test"}); err == nil {
		t.Error("a duplicate email must be refused")
	}
	t.Setenv("BLASTA_NEW_PASSWORD", "short")
	if err := cmdUser([]string{"reset-password", "--data-dir", dir, "--email", "pat@acme.test"}); err == nil {
		t.Error("a weak password must be refused")
	}
	t.Setenv("BLASTA_NEW_PASSWORD", "a brand new passphrase")
	if err := cmdUser([]string{"reset-password", "--data-dir", dir, "--email", "pat@acme.test"}); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := cmdUser([]string{"set-role", "--data-dir", dir, "--email", "root@acme.test", "--role", "user"}); err == nil ||
		!strings.Contains(err.Error(), "administrator") {
		t.Errorf("demoting the only admin must be refused, got %v", err)
	}
	if err := cmdUser([]string{"reset-password", "--data-dir", dir, "--email", "ghost@acme.test"}); err == nil {
		t.Error("an unknown account must be reported")
	}
	if err := cmdUser([]string{"create", "--email", "x@acme.test"}); err == nil {
		t.Error("a data directory is required")
	}
	// The result is real: the password actually works for a fresh service.
	d, err := db.Open("", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	svc, _ := auth.New(auth.NewStore(d), auth.Config{})
	if _, _, err := svc.Login("pat@acme.test", "a brand new passphrase", "1.1.1.1"); err != nil {
		t.Errorf("the reset password must work: %v", err)
	}
}
