package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/awhadi/blasta-perftest/internal/settings"
)

// Bot checks. The visitor solves a challenge in the browser; the page sends the
// resulting token with the request, and the server asks the provider whether it is
// genuine. Anything that goes wrong (provider down, no token, wrong token) counts
// as "not verified": the whole point is to keep automated traffic out.

// captchaEndpoints are where tokens are verified. A variable so tests can point it
// at a fake provider.
var captchaEndpoints = map[string]string{
	"turnstile": "https://challenges.cloudflare.com/turnstile/v0/siteverify",
	"recaptcha": "https://www.google.com/recaptcha/api/siteverify",
}

// ErrCaptcha is returned when a required bot check was not passed.
var ErrCaptcha = errors.New("please complete the bot check and try again")

// ErrGuestCaptcha is returned when a visitor must pass the bot check before a test.
var ErrGuestCaptcha = errors.New("please complete the bot check before your first test")

// Captcha scopes: where the check is asked for.
const (
	ScopeLogin    = "login"
	ScopeRegister = "register"
	ScopeGuest    = "guest"
)

func captchaReady(c settings.Captcha) bool {
	return c.Enabled && c.SiteKey != "" && c.Secret != "" && captchaEndpoints[c.Provider] != ""
}

// CaptchaRequired reports whether a scope is protected right now.
func (s *Service) CaptchaRequired(scope string) bool {
	c := s.conf().Captcha
	if !captchaReady(c) {
		return false
	}
	switch scope {
	case ScopeLogin:
		return c.OnLogin
	case ScopeRegister:
		return c.OnRegister
	case ScopeGuest:
		return c.OnGuest
	}
	return false
}

// verifyToken asks the provider whether a token is genuine. It also returns the
// provider's error codes, which tell a bad token from a bad secret key.
func verifyToken(ctx context.Context, provider, secret, token, ip string) (bool, []string, error) {
	endpoint := captchaEndpoints[provider]
	if endpoint == "" {
		return false, nil, errors.New("unknown bot-check provider")
	}
	form := url.Values{"secret": {secret}, "response": {token}}
	if ip != "" {
		form.Set("remoteip", ip)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return false, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, nil, errors.New("could not reach the bot-check provider: " + err.Error())
	}
	defer resp.Body.Close()
	var out struct {
		Success bool     `json:"success"`
		Codes   []string `json:"error-codes"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out); err != nil {
		return false, nil, errors.New("the bot-check provider sent an unreadable answer")
	}
	return out.Success, out.Codes, nil
}

// checkCaptcha enforces the bot check for a scope. It does nothing when the scope
// is not protected.
func (s *Service) checkCaptcha(r *http.Request, scope, token string) error {
	if !s.CaptchaRequired(scope) {
		return nil
	}
	if strings.TrimSpace(token) == "" {
		return ErrCaptcha
	}
	c := s.conf().Captcha
	ok, _, err := verifyToken(r.Context(), c.Provider, c.Secret, token, s.clientIP(r))
	if err != nil || !ok {
		return ErrCaptcha
	}
	return nil
}

// CheckCaptchaSecret tells whether a secret key is accepted by the provider,
// without needing a solved challenge: a made-up token is rejected as a bad
// token if the key is fine, and as a bad key if it is not.
func CheckCaptchaSecret(ctx context.Context, provider, secret string) error {
	if strings.TrimSpace(secret) == "" {
		return errors.New("enter the secret key")
	}
	ok, codes, err := verifyToken(ctx, provider, secret, "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "")
	if err != nil {
		return err
	}
	for _, c := range codes {
		if c == "invalid-input-secret" || c == "missing-input-secret" {
			return errors.New("the provider rejected this secret key")
		}
	}
	_ = ok
	return nil
}

// CaptchaCSP lists what the page may load for the active provider's widget. The
// page is otherwise locked to itself, so nothing is allowed unless a check is on.
func (s *Service) CaptchaCSP() (script, frame, style, img, connect []string) {
	c := s.conf().Captcha
	if !captchaReady(c) || !(c.OnLogin || c.OnRegister || c.OnGuest) {
		return
	}
	switch c.Provider {
	case "turnstile":
		return []string{"https://challenges.cloudflare.com"}, []string{"https://challenges.cloudflare.com"}, nil, nil, nil
	case "recaptcha":
		return []string{"https://www.google.com", "https://www.gstatic.com"},
			[]string{"https://www.google.com", "https://recaptcha.google.com"},
			[]string{"'unsafe-inline'"}, []string{"https://www.gstatic.com"}, []string{"https://www.google.com"}
	}
	return
}

// publicCaptcha is what the sign-in page needs to show the challenge.
type publicCaptcha struct {
	Provider   string `json:"provider"`
	SiteKey    string `json:"siteKey"`
	OnLogin    bool   `json:"onLogin"`
	OnRegister bool   `json:"onRegister"`
	OnGuest    bool   `json:"onGuest"`
}

func (s *Service) publicCaptcha() *publicCaptcha {
	c := s.conf().Captcha
	if !captchaReady(c) {
		return nil
	}
	return &publicCaptcha{Provider: c.Provider, SiteKey: c.SiteKey, OnLogin: c.OnLogin, OnRegister: c.OnRegister, OnGuest: c.OnGuest}
}
