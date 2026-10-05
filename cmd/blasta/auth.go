package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/awhadi/blasta-perftest/internal/auth"
	"github.com/awhadi/blasta-perftest/internal/db"
	"github.com/awhadi/blasta-perftest/internal/mailer"
	"github.com/awhadi/blasta-perftest/internal/settings"
)

// envBool reads a boolean environment variable; def applies when it is unset.
func envBool(name string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func envList(name string) []string {
	var out []string
	for _, p := range strings.Split(os.Getenv(name), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envDuration(name string, def time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a duration such as 12h", name)
	}
	return d, nil
}

// authConfigFromEnv builds the sign-in configuration. Secrets (the setup token
// and the SSO client secret) come from the environment only, never from flags,
// so they do not show up in process listings.
func authConfigFromEnv(publicURL string) (auth.Config, error) {
	cfg := auth.Config{
		Registration:   strings.ToLower(strings.TrimSpace(os.Getenv("BLASTA_REGISTRATION"))),
		AllowedDomains: envList("BLASTA_ALLOWED_EMAIL_DOMAINS"),
		SetupToken:     os.Getenv("BLASTA_SETUP_TOKEN"),
		PublicURL:      publicURL,
	}
	cfg.Guest = settings.DefaultGuest()
	cfg.Guest.Enabled = envBool("BLASTA_GUEST", true)
	if host := os.Getenv("BLASTA_SMTP_HOST"); host != "" {
		port, _ := strconv.Atoi(os.Getenv("BLASTA_SMTP_PORT"))
		if port == 0 {
			port = 587
		}
		sec := strings.ToLower(os.Getenv("BLASTA_SMTP_SECURITY"))
		if sec == "" {
			sec = "starttls"
		}
		cfg.SMTP = mailer.Config{Host: host, Port: port, Security: sec, Username: os.Getenv("BLASTA_SMTP_USER"),
			Password: os.Getenv("BLASTA_SMTP_PASSWORD"), From: os.Getenv("BLASTA_SMTP_FROM"), FromName: os.Getenv("BLASTA_SMTP_FROM_NAME")}
	}
	cfg.CaptchaOff = envBool("BLASTA_CAPTCHA_OFF", false)
	cfg.OTPLogin = envBool("BLASTA_OTP_LOGIN", false)
	cfg.DisableReset = envBool("BLASTA_DISABLE_PASSWORD_RESET", false)
	if site, secret := os.Getenv("BLASTA_CAPTCHA_SITE_KEY"), os.Getenv("BLASTA_CAPTCHA_SECRET_KEY"); site != "" && secret != "" {
		prov := strings.ToLower(os.Getenv("BLASTA_CAPTCHA_PROVIDER"))
		if prov == "" {
			prov = "turnstile"
		}
		cfg.Captcha = settings.Captcha{Enabled: envBool("BLASTA_CAPTCHA", true), Provider: prov, SiteKey: site, Secret: secret,
			OnLogin: true, OnRegister: true, OnGuest: true}
	}
	var err error
	if cfg.TrustedProxies, err = parseProxies(envList("BLASTA_TRUSTED_PROXIES")); err != nil {
		return cfg, err
	}
	if cfg.SessionIdle, err = envDuration("BLASTA_SESSION_IDLE", 0); err != nil {
		return cfg, err
	}
	if cfg.SessionMax, err = envDuration("BLASTA_SESSION_MAX", 0); err != nil {
		return cfg, err
	}
	if issuer := os.Getenv("BLASTA_OIDC_ISSUER"); issuer != "" {
		cfg.SSO = &auth.OIDCConfig{
			Name:          os.Getenv("BLASTA_OIDC_NAME"),
			Issuer:        issuer,
			DiscoveryURL:  os.Getenv("BLASTA_OIDC_DISCOVERY_URL"),
			ClientID:      os.Getenv("BLASTA_OIDC_CLIENT_ID"),
			ClientSecret:  os.Getenv("BLASTA_OIDC_CLIENT_SECRET"),
			Scopes:        os.Getenv("BLASTA_OIDC_SCOPES"),
			AllowInsecure: envBool("BLASTA_OIDC_ALLOW_INSECURE", false),
			AutoCreate:    envBool("BLASTA_OIDC_AUTO_CREATE", true),
			Trust:         strings.ToLower(os.Getenv("BLASTA_OIDC_TRUST")),
			AdminEmails:   envList("BLASTA_OIDC_ADMIN_EMAILS"),
			AdminGroup:    os.Getenv("BLASTA_OIDC_ADMIN_GROUP"),
			GroupsClaim:   os.Getenv("BLASTA_OIDC_GROUPS_CLAIM"),
		}
	}
	return cfg, nil
}

// cmdUser manages accounts from the command line: the way to create the first
// administrator without the web page, and to recover a lost password. It edits
// the same data directory the server uses, and a running server notices.
func cmdUser(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: blasta user <create|reset-password|list|set-role> [flags]")
	}
	fs := flag.NewFlagSet("user "+args[0], flag.ExitOnError)
	dataDir := fs.String("data-dir", os.Getenv("BLASTA_DATA_DIR"), "data directory (env BLASTA_DATA_DIR)")
	dbURL := fs.String("database-url", os.Getenv("BLASTA_DATABASE_URL"), "database URL (env BLASTA_DATABASE_URL)")
	email := fs.String("email", "", "the account's email address")
	name := fs.String("name", "", "display name (create)")
	admin := fs.Bool("admin", false, "make the account an administrator (create)")
	role := fs.String("role", "", "admin or user (set-role)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *dataDir == "" && *dbURL == "" {
		return errors.New("say where the database is: pass --data-dir or set BLASTA_DATA_DIR (or BLASTA_DATABASE_URL)")
	}
	d, err := db.Open(*dbURL, *dataDir)
	if err != nil {
		return err
	}
	defer d.Close()
	st := auth.NewStore(d)
	if _, err := st.ImportLegacy(*dataDir); err != nil {
		return err
	}
	svc, err := auth.New(st, auth.Config{})
	if err != nil {
		return err
	}

	switch args[0] {
	case "list":
		for _, u := range st.Users() {
			fmt.Printf("%-34s %-6s %-9s %s\n", u.Email, u.Role, u.Status, u.Name)
		}
		return nil
	case "create":
		pw, err := readPassword()
		if err != nil {
			return err
		}
		r := auth.RoleUser
		if *admin {
			r = auth.RoleAdmin
		}
		u, err := svc.CreateUser(*email, *name, pw, r)
		if err != nil {
			return err
		}
		fmt.Printf("created %s (%s)\n", u.Email, u.Role)
		return nil
	case "reset-password":
		u := st.UserByEmail(*email)
		if u == nil {
			return fmt.Errorf("no account with email %q", *email)
		}
		pw, err := readPassword()
		if err != nil {
			return err
		}
		if err := svc.SetPassword(u.ID, pw); err != nil {
			return err
		}
		fmt.Printf("password changed for %s; every session was signed out\n", u.Email)
		return nil
	case "set-role":
		u := st.UserByEmail(*email)
		if u == nil {
			return fmt.Errorf("no account with email %q", *email)
		}
		if _, err := svc.SetRole(u.ID, *role); err != nil {
			return err
		}
		fmt.Printf("%s is now %s\n", u.Email, *role)
		return nil
	}
	return fmt.Errorf("unknown user command %q", args[0])
}

// readPassword takes the password from BLASTA_NEW_PASSWORD, or the first line on
// standard input. It is never a flag, so it does not appear in the process list
// or shell history.
func readPassword() (string, error) {
	if v := os.Getenv("BLASTA_NEW_PASSWORD"); v != "" {
		return v, nil
	}
	fmt.Fprintln(os.Stderr, "password (one line, then Enter):")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no password given: set BLASTA_NEW_PASSWORD or pipe it in")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// parseProxies reads the trusted reverse proxies: addresses, CIDR ranges, or the
// word "private" for loopback and the private ranges (which covers Docker and
// most Kubernetes networks).
func parseProxies(items []string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	add := func(cidr string) {
		if _, n, err := net.ParseCIDR(cidr); err == nil {
			out = append(out, n)
		}
	}
	for _, it := range items {
		switch {
		case strings.EqualFold(it, "private"):
			for _, c := range []string{"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7", "169.254.0.0/16"} {
				add(c)
			}
		case strings.Contains(it, "/"):
			_, n, err := net.ParseCIDR(it)
			if err != nil {
				return nil, fmt.Errorf("BLASTA_TRUSTED_PROXIES: %q is not a valid range", it)
			}
			out = append(out, n)
		default:
			ip := net.ParseIP(it)
			if ip == nil {
				return nil, fmt.Errorf("BLASTA_TRUSTED_PROXIES: %q is not an address, a range or \"private\"", it)
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
	}
	return out, nil
}
