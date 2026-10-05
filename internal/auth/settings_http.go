package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/awhadi/blasta-perftest/internal/mailer"
	"github.com/awhadi/blasta-perftest/internal/settings"
)

// The settings pages never receive a stored secret: only whether one is set.
type ssoView struct {
	settings.SSO
	SecretSet bool `json:"clientSecretSet"`
}
type smtpView struct {
	settings.SMTP
	PasswordSet bool `json:"passwordSet"`
}

type captchaView struct {
	settings.Captcha
	SecretSet bool `json:"secretSet"`
	Off       bool `json:"envOff"` // BLASTA_CAPTCHA_OFF is set
}

func (s *Service) settingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/settings", s.handleGetSettings)
	mux.HandleFunc("POST /api/admin/settings/general", s.saveGeneral)
	mux.HandleFunc("POST /api/admin/settings/sso", s.saveSSO)
	mux.HandleFunc("POST /api/admin/settings/smtp", s.saveSMTP)
	mux.HandleFunc("POST /api/admin/settings/guest", s.saveGuest)
	mux.HandleFunc("POST /api/admin/settings/captcha", s.saveCaptcha)
	mux.HandleFunc("POST /api/admin/settings/captcha/verify", s.verifyCaptchaKey)
	mux.HandleFunc("POST /api/admin/settings/sso/test", s.testSSO)
	mux.HandleFunc("POST /api/admin/settings/smtp/test", s.testSMTP)
	mux.HandleFunc("POST /api/admin/settings/smtp/verify", s.verifySMTP)
	mux.HandleFunc("DELETE /api/admin/settings/{section}", s.resetSection)
}

func (s *Service) needSettings(w http.ResponseWriter) bool {
	if s.settings == nil {
		writeErr(w, 409, "settings are not available: BLASTA has no database")
		return false
	}
	return true
}

// effective returns each section as currently in force, and whether it came
// from saved settings (true) or the environment (false).
func (s *Service) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	if !s.needSettings(w) {
		return
	}
	cfg := s.conf()
	saved := map[string]bool{}
	for _, k := range []string{settings.KeyGeneral, settings.KeySSO, settings.KeySMTP, settings.KeyGuest, settings.KeyCaptcha} {
		var raw map[string]any
		ok, _ := s.settings.Get(k, &raw)
		saved[k] = ok
	}
	sso := ssoView{SSO: settings.SSO{AutoCreate: true, Scopes: "openid email profile"}}
	if c := cfg.SSO; c != nil {
		sso = ssoView{SSO: settings.SSO{Enabled: true, Name: c.Name, Issuer: c.Issuer, DiscoveryURL: c.DiscoveryURL,
			ClientID: c.ClientID, Scopes: c.Scopes, AllowInsecure: c.AllowInsecure, AutoCreate: c.AutoCreate,
			Trust: c.Trust, AdminEmails: c.AdminEmails, AdminGroup: c.AdminGroup, GroupsClaim: c.GroupsClaim},
			SecretSet: c.ClientSecret != ""}
	}
	// A section that was saved but switched off is not part of the running config, yet the form
	// must still show what was typed, or saving with the toggle off looks like losing it all.
	if v, ok, err := s.settings.GetSSO(); err == nil && ok && !v.Enabled {
		sso = ssoView{SSO: settings.SSO{Name: v.Name, Issuer: v.Issuer, DiscoveryURL: v.DiscoveryURL,
			ClientID: v.ClientID, Scopes: v.Scopes, AllowInsecure: v.AllowInsecure, AutoCreate: v.AutoCreate,
			Trust: v.Trust, AdminEmails: v.AdminEmails, AdminGroup: v.AdminGroup, GroupsClaim: v.GroupsClaim},
			SecretSet: v.ClientSecret != ""}
	}
	m := cfg.SMTP
	mail := smtpView{SMTP: settings.SMTP{Enabled: m.Host != "", Host: m.Host, Port: m.Port, Security: m.Security,
		Username: m.Username, From: m.From, FromName: senderName(m.FromName)}, PasswordSet: m.Password != ""}
	if v, ok, err := s.settings.GetSMTP(); err == nil && ok && !v.Enabled {
		mail = smtpView{SMTP: settings.SMTP{Host: v.Host, Port: v.Port, Security: v.Security,
			Username: v.Username, From: v.From, FromName: senderName(v.FromName)}, PasswordSet: v.Password != ""}
	}
	if mail.Port == 0 {
		mail.Port, mail.Security = 587, "starttls"
	}
	writeJSON(w, 200, map[string]any{
		"general":     settings.General{PublicURL: cfg.PublicURL, Registration: cfg.Registration, AllowedDomains: cfg.AllowedDomains, OTPLogin: cfg.OTPLogin, DisableReset: cfg.DisableReset},
		"sso":         sso,
		"smtp":        mail,
		"guest":       cfg.Guest,
		"mailProblem": s.LastMailProblem(),
		"captcha":     captchaView{Captcha: cfg.Captcha, SecretSet: cfg.Captcha.Secret != "", Off: s.base.CaptchaOff},
		"saved":       saved,
		"redirectUrl": s.redirectURL(r),
	})
}

// commit saves a section and reloads. If the result will not load, the old
// saved value is put back so a typo cannot lock everyone out of sign-in.
func (s *Service) commit(w http.ResponseWriter, key string, save func() error) {
	var prev map[string]any
	had, _ := s.settings.Get(key, &prev)
	if err := save(); err != nil {
		s.lg().Error("settings could not be saved", "section", key, "err", err.Error())
		writeErr(w, 500, "could not save: "+err.Error())
		return
	}
	if err := s.Reload(); err != nil {
		s.lg().Warn("settings refused and rolled back", "section", key, "err", err.Error())
		if had {
			_ = s.settings.Put(key, prev)
		} else {
			_ = s.settings.Delete(key)
		}
		_ = s.Reload()
		writeErr(w, 400, err.Error())
		return
	}
	s.lg().Info("settings saved", "section", key)
	writeJSON(w, 200, map[string]string{"status": "saved"})
}

func cleanList(in []string) []string {
	out := []string{}
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func validPublicURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/" && CleanBase(u.Path) == "") {
		return errors.New("the public URL must look like https://blasta.example.com or https://example.com/blasta")
	}
	return nil
}

func (s *Service) saveGeneral(w http.ResponseWriter, r *http.Request) {
	if !s.needSettings(w) {
		return
	}
	var in settings.General
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	in.PublicURL = strings.TrimRight(strings.TrimSpace(in.PublicURL), "/")
	in.AllowedDomains = cleanList(in.AllowedDomains)
	if err := validPublicURL(in.PublicURL); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if in.OTPLogin && !s.Mailer().Ready() {
		writeErr(w, 400, "set up Email Delivery first: the one-time codes are sent by email")
		return
	}
	s.commit(w, settings.KeyGeneral, func() error { return s.settings.Put(settings.KeyGeneral, in) })
}

func (s *Service) ssoFromRequest(r *http.Request) (settings.SSO, error) {
	var in struct {
		settings.SSO
		ClientSecret string `json:"clientSecret"`
		ClearSecret  bool   `json:"clearSecret"` // forget the stored secret (removing the provider)
	}
	if err := decode(r, &in); err != nil {
		return in.SSO, errors.New("invalid request")
	}
	v := in.SSO
	v.ClientSecret = in.ClientSecret
	v.AdminEmails = cleanList(v.AdminEmails)
	v.Issuer = strings.TrimRight(strings.TrimSpace(v.Issuer), "/")
	if v.ClientSecret == "" && !in.ClearSecret { // blank keeps the stored secret
		if old, ok, _ := s.settings.GetSSO(); ok {
			v.ClientSecret = old.ClientSecret
		} else if c := s.conf().SSO; c != nil {
			v.ClientSecret = c.ClientSecret
		}
	}
	return v, nil
}

func toOIDC(v settings.SSO) OIDCConfig {
	return OIDCConfig{Name: v.Name, Issuer: v.Issuer, DiscoveryURL: v.DiscoveryURL, ClientID: v.ClientID,
		ClientSecret: v.ClientSecret, Scopes: v.Scopes, AllowInsecure: v.AllowInsecure, AutoCreate: v.AutoCreate,
		Trust: v.Trust, AdminEmails: v.AdminEmails, AdminGroup: v.AdminGroup, GroupsClaim: v.GroupsClaim}
}

func (s *Service) saveSSO(w http.ResponseWriter, r *http.Request) {
	if !s.needSettings(w) {
		return
	}
	v, err := s.ssoFromRequest(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if v.Enabled {
		if _, err := newOIDCClient(toOIDC(v)); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}
	s.commit(w, settings.KeySSO, func() error { return s.settings.PutSSO(v) })
}

// testSSO asks the provider for its discovery document, which proves the issuer
// URL, the network path and (for a strict provider) nothing about the client
// credentials: those are only checked by a real sign-in.
func (s *Service) testSSO(w http.ResponseWriter, r *http.Request) {
	if !s.needSettings(w) {
		return
	}
	v, err := s.ssoFromRequest(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	c, err := newOIDCClient(toOIDC(v))
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	d, err := c.discover(ctx)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "the provider answered", "issuer": d.Issuer,
		"authorizationEndpoint": d.AuthorizationEndpoint, "tokenEndpoint": d.TokenEndpoint})
}

func (s *Service) smtpFromRequest(r *http.Request) (settings.SMTP, error) {
	var in struct {
		settings.SMTP
		Password string `json:"password"`
		To       string `json:"to"`
	}
	if err := decode(r, &in); err != nil {
		return in.SMTP, errors.New("invalid request")
	}
	v := in.SMTP
	v.Password = in.Password
	v.Host, v.Username, v.From = strings.TrimSpace(v.Host), strings.TrimSpace(v.Username), strings.TrimSpace(v.From)
	if v.Password == "" {
		if old, ok, _ := s.settings.GetSMTP(); ok {
			v.Password = old.Password
		} else {
			v.Password = s.conf().SMTP.Password
		}
	}
	return v, nil
}

func toMailer(v settings.SMTP) mailer.Config {
	return mailer.Config{Host: v.Host, Port: v.Port, Security: v.Security, Username: v.Username, Password: v.Password, From: v.From, FromName: senderName(v.FromName)}
}

func (s *Service) saveSMTP(w http.ResponseWriter, r *http.Request) {
	if !s.needSettings(w) {
		return
	}
	v, err := s.smtpFromRequest(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if v.Enabled {
		if err := toMailer(v).Validate(); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}
	s.commit(w, settings.KeySMTP, func() error { return s.settings.PutSMTP(v) })
}

// testSMTP sends a real message with the form's values (before they are saved).
func (s *Service) testSMTP(w http.ResponseWriter, r *http.Request) {
	if !s.needSettings(w) {
		return
	}
	v, err := s.smtpFromRequest(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	u := UserFrom(r.Context())
	if u == nil {
		writeErr(w, 401, "authentication required", "unauthenticated")
		return
	}
	// Always to the signed-in administrator: this button must not be a way to
	// send mail to arbitrary people through the configured server.
	text, html := themed("Email works", []string{"This is a test message from BLASTA. If you can read it, outgoing email is set up correctly, and BLASTA can send confirmation and password-reset links."}, "", "",
		"Sent from Administration > Settings > Email Delivery.")
	err = mailer.SendHTML(toMailer(v), []string{u.Email}, "BLASTA test email", text, html)
	if err != nil {
		s.lg().Warn("test email failed", "host", v.Host, "port", v.Port, "security", v.Security, "err", err.Error())
		writeErr(w, 400, err.Error())
		return
	}
	s.lg().Info("test email sent", "host", v.Host, "to", maskAddr(u.Email))
	writeJSON(w, 200, map[string]string{"status": "sent to " + u.Email})
}

func (s *Service) saveGuest(w http.ResponseWriter, r *http.Request) {
	if !s.needSettings(w) {
		return
	}
	var in settings.Guest
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	for _, c := range []struct {
		v        *int
		name     string
		min, max int
	}{
		{&in.TrialMinutes, "trial length (minutes)", 1, 1440},
		{&in.MaxRuns, "runs per trial", 1, 100},
		{&in.MaxDurationSec, "longest run (seconds)", 1, 300},
		{&in.MaxRPS, "requests per second", 1, 5000},
		{&in.MaxConcurrency, "connections", 1, 500},
		{&in.DailyPerIP, "runs per network per day", 1, 10000},
	} {
		if *c.v < c.min || *c.v > c.max {
			writeErr(w, 400, c.name+" must be between "+strconv.Itoa(c.min)+" and "+strconv.Itoa(c.max))
			return
		}
	}
	s.commit(w, settings.KeyGuest, func() error { return s.settings.Put(settings.KeyGuest, in) })
}

func (s *Service) captchaFromRequest(r *http.Request) (settings.Captcha, error) {
	var in struct {
		settings.Captcha
		SecretKey string `json:"secretKey"`
	}
	if err := decode(r, &in); err != nil {
		return in.Captcha, errors.New("invalid request")
	}
	v := in.Captcha
	v.Secret = strings.TrimSpace(in.SecretKey)
	v.SiteKey = strings.TrimSpace(v.SiteKey)
	if v.Provider == "" {
		v.Provider = "turnstile"
	}
	if v.Secret == "" { // blank keeps the stored secret
		if old, ok, _ := s.settings.GetCaptcha(); ok {
			v.Secret = old.Secret
		} else {
			v.Secret = s.conf().Captcha.Secret
		}
	}
	return v, nil
}

// saveCaptcha stores the bot-check settings. Turning it on needs a secret key the
// provider accepts: a wrong key would lock everyone out of sign-in.
func (s *Service) saveCaptcha(w http.ResponseWriter, r *http.Request) {
	if !s.needSettings(w) {
		return
	}
	v, err := s.captchaFromRequest(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if captchaEndpoints[v.Provider] == "" {
		writeErr(w, 400, "choose Cloudflare Turnstile or Google reCAPTCHA")
		return
	}
	if v.Enabled {
		if v.SiteKey == "" || v.Secret == "" {
			writeErr(w, 400, "enter both the site key and the secret key before turning bot protection on")
			return
		}
		if !(v.OnLogin || v.OnRegister || v.OnGuest) {
			writeErr(w, 400, "choose where the bot check should appear")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		if err := CheckCaptchaSecret(ctx, v.Provider, v.Secret); err != nil {
			writeErr(w, 400, "bot protection was not turned on: "+err.Error())
			return
		}
	}
	s.commit(w, settings.KeyCaptcha, func() error { return s.settings.PutCaptcha(v) })
}

// verifyCaptchaKey checks a secret key as typed, before anything is saved.
func (s *Service) verifyCaptchaKey(w http.ResponseWriter, r *http.Request) {
	if !s.needSettings(w) {
		return
	}
	v, err := s.captchaFromRequest(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	if err := CheckCaptchaSecret(ctx, v.Provider, v.Secret); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "the provider accepted this secret key"})
}

// resetSection forgets saved settings so the environment's apply again.
func (s *Service) resetSection(w http.ResponseWriter, r *http.Request) {
	if !s.needSettings(w) {
		return
	}
	switch k := r.PathValue("section"); k {
	case settings.KeyGeneral, settings.KeySSO, settings.KeySMTP, settings.KeyGuest, settings.KeyCaptcha:
		_ = s.settings.Delete(k)
		if err := s.Reload(); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]string{"status": "reset"})
	default:
		writeErr(w, 404, "no such section")
	}
}

// verifySMTP checks the connection settings in the form, before they are saved,
// without sending anything.
func (s *Service) verifySMTP(w http.ResponseWriter, r *http.Request) {
	if !s.needSettings(w) {
		return
	}
	v, err := s.smtpFromRequest(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	msg, err := mailer.Verify(toMailer(v))
	if err != nil {
		s.lg().Warn("mail server check failed", "host", v.Host, "port", v.Port, "security", v.Security, "err", err.Error())
		writeErr(w, 400, err.Error())
		return
	}
	s.lg().Info("mail server check passed", "host", v.Host, "port", v.Port, "security", v.Security)
	writeJSON(w, 200, map[string]string{"status": msg})
}

func senderName(n string) string {
	if n = strings.TrimSpace(n); n != "" {
		return n
	}
	return DefaultSenderName
}
