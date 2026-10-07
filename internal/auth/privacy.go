package auth

import (
	"net/http"
	"strings"

	"github.com/awhadi/blasta-perftest/internal/analytics"
	"github.com/awhadi/blasta-perftest/internal/privacy"
	"github.com/awhadi/blasta-perftest/internal/settings"
)

func privacyModes() []map[string]string {
	out := make([]map[string]string, len(privacy.Modes))
	for i, m := range privacy.Modes {
		out[i] = map[string]string{"id": m.ID, "name": m.Name, "help": m.Help}
	}
	return out
}

func (s *Service) savePrivacy(w http.ResponseWriter, r *http.Request) {
	if !s.needSettings(w) {
		return
	}
	var in settings.Privacy
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	v, err := privacy.Clean(in)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	s.commit(w, settings.KeyPrivacy, func() error { return s.settings.Put(settings.KeyPrivacy, v) })
}

// analyticsFor is the analytics service that applies to this visit ("" if none): the one the
// administrator switched on, unless this person is signed in and not to be counted.
func (s *Service) analyticsFor(r *http.Request) string {
	c := s.conf().Analytics
	if !analytics.Ready(c) {
		return ""
	}
	if !c.TrackSignedIn {
		if ck, err := r.Cookie(SessionCookie); err == nil {
			if _, err := s.Authenticate(ck.Value); err == nil {
				return ""
			}
		}
	}
	return analytics.Name(c.Provider)
}

// PrivacyNeeded reports whether pages must load the consent script: there is analytics to ask
// about, or a notice to show.
func (s *Service) PrivacyNeeded() bool {
	cfg := s.conf()
	return privacy.NeedsScript(cfg.Privacy, analytics.Ready(cfg.Analytics))
}

// PrivacyScript is the consent banner, served at /consent.js.
func (s *Service) PrivacyScript(r *http.Request) string {
	return privacy.Script(s.conf().Privacy, s.analyticsFor(r))
}

// PrivacyPage is the privacy page, made from the site's settings.
func (s *Service) PrivacyPage(baseHref, canonical string) []byte {
	cfg := s.conf()
	d := privacy.PageData{Privacy: cfg.Privacy, Base: baseHref, Canonical: canonical, Guest: cfg.Guest.Enabled,
		SessionDays: int(cfg.SessionMax.Hours()/24 + 0.5)}
	if d.SessionDays < 1 {
		d.SessionDays = 1
	}
	n := cfg.Notifications
	d.NotifyEmail = n.Enabled && n.EmailRunner
	d.NotifyShared = n.Enabled && (n.Slack != "" || n.Teams != "" || n.Webhook != "")
	if analytics.Ready(cfg.Analytics) {
		d.AnalyticsName = analytics.Name(cfg.Analytics.Provider)
	}
	if cfg.SSO != nil {
		d.SSO = strings.TrimSpace(cfg.SSO.Name)
		if d.SSO == "" {
			d.SSO = "single sign-on"
		}
	}
	if c := cfg.Captcha; captchaReady(c) && (c.OnLogin || c.OnRegister || c.OnGuest) {
		d.Captcha = map[string]string{"turnstile": "Cloudflare Turnstile", "recaptcha": "Google reCAPTCHA"}[c.Provider]
	}
	return privacy.Page(d)
}
