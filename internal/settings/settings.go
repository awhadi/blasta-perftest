package settings

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/awhadi/blasta-perftest/internal/db"
)

// Section names in the settings table.
const (
	KeyGeneral       = "general"
	KeySSO           = "sso"
	KeySMTP          = "smtp"
	KeyGuest         = "guest"
	KeyCaptcha       = "captcha"
	KeyAnalytics     = "analytics"
	KeyPrivacy       = "privacy"
	KeyNotifications = "notifications"
)

// General is how people sign up and where BLASTA is reached.
type General struct {
	PublicURL      string   `json:"publicUrl"`
	Registration   string   `json:"registration"` // open | approval | closed
	AllowedDomains []string `json:"allowedDomains"`
	OTPLogin       bool     `json:"otpLogin"`     // sign in with a one-time code sent by email
	DisableReset   bool     `json:"disableReset"` // turn off "forgot password" (on by default)
}

// SSO is an OpenID Connect provider. The client secret is stored sealed.
type SSO struct {
	Enabled       bool     `json:"enabled"`
	Name          string   `json:"name"`
	Issuer        string   `json:"issuer"`
	DiscoveryURL  string   `json:"discoveryUrl"`
	ClientID      string   `json:"clientId"`
	ClientSecret  string   `json:"-"` // plain, in memory only
	SecretSealed  string   `json:"clientSecretSealed"`
	Scopes        string   `json:"scopes"`
	AllowInsecure bool     `json:"allowInsecure"`
	AutoCreate    bool     `json:"autoCreate"`
	Trust         string   `json:"trust"` // "" or "all"
	AdminEmails   []string `json:"adminEmails"`
	AdminGroup    string   `json:"adminGroup"`
	GroupsClaim   string   `json:"groupsClaim"`
}

// SMTP is the outgoing mail server. The password is stored sealed.
type SMTP struct {
	Enabled        bool   `json:"enabled"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Security       string `json:"security"` // starttls | tls | none
	Username       string `json:"username"`
	Password       string `json:"-"`
	PasswordSealed string `json:"passwordSealed"`
	From           string `json:"from"`
	FromName       string `json:"fromName"` // the sender text shown in mail apps
}

// Guest limits what someone who has not registered can do.
type Guest struct {
	Enabled        bool `json:"enabled"`
	TrialMinutes   int  `json:"trialMinutes"`   // how long a visitor's trial lasts
	MaxRuns        int  `json:"maxRuns"`        // runs per trial
	MaxDurationSec int  `json:"maxDurationSec"` // longest single run
	MaxRPS         int  `json:"maxRps"`
	MaxConcurrency int  `json:"maxConcurrency"`
	DailyPerIP     int  `json:"dailyPerIp"` // runs per address per day, whoever they are
}

// DefaultGuest is used until an administrator changes it.
func DefaultGuest() Guest {
	return Guest{Enabled: true, TrialMinutes: 15, MaxRuns: 3, MaxDurationSec: 30, MaxRPS: 50, MaxConcurrency: 20, DailyPerIP: 20}
}

// Captcha is the bot check (Cloudflare Turnstile or Google reCAPTCHA). The secret
// key is stored sealed; the site key is public by design.
type Captcha struct {
	Enabled      bool   `json:"enabled"`
	Provider     string `json:"provider"` // turnstile | recaptcha
	SiteKey      string `json:"siteKey"`
	Secret       string `json:"-"`
	SecretSealed string `json:"secretSealed"`
	OnLogin      bool   `json:"onLogin"`    // sign-in and password reset
	OnRegister   bool   `json:"onRegister"` // creating an account
	OnGuest      bool   `json:"onGuest"`    // a visitor's first free test
}

// Analytics is the visitor analytics an administrator can switch on: one provider, loaded by a
// script BLASTA serves itself (the page's security policy allows no inline code), with only the
// hosts that provider needs let through. Nothing here is secret: the ids appear in the page.
type Analytics struct {
	Enabled       bool     `json:"enabled"`
	Provider      string   `json:"provider"`      // ga4 | gtm | plausible | umami | matomo | cloudflare | custom
	ID            string   `json:"id"`            // measurement, container, site or website id; the domain for Plausible
	ScriptURL     string   `json:"scriptUrl"`     // Plausible host, Umami script, Matomo address, or the custom script
	ExtraHosts    []string `json:"extraHosts"`    // more https hosts the provider's script talks to
	RespectDNT    bool     `json:"respectDnt"`    // stay off for people who send Do Not Track or Global Privacy Control
	TrackSignedIn bool     `json:"trackSignedIn"` // also count people who are signed in (off: only visitors)
}

// Privacy is how the site deals with cookie consent and says what it stores. Nothing here is
// secret. It does not decide what the law requires: that is for the site's operator.
type Privacy struct {
	Mode       string `json:"mode"`       // optin | optout | notice | off
	Message    string `json:"message"`    // the banner text; empty: BLASTA's own
	PolicyURL  string `json:"policyUrl"`  // the operator's own policy; empty: BLASTA's /privacy page
	Controller string `json:"controller"` // who runs this site, for the privacy page
	Contact    string `json:"contact"`    // where to send privacy requests
	Notes      string `json:"notes"`      // extra paragraphs for the privacy page
	// NoSelfDelete stops people deleting their own account and test history (off by default:
	// they can, as most data protection laws expect).
	NoSelfDelete bool `json:"noSelfDelete"`
}

// Notifications is how the site tells people that a test has finished: set once by an
// administrator, for everyone. The webhook addresses are secrets (whoever has one can post to
// that channel), so they are stored sealed.
type Notifications struct {
	Enabled     bool     `json:"enabled"`
	On          string   `json:"on"`          // always | problems
	EmailRunner bool     `json:"emailRunner"` // email the person who started the test
	EmailTo     []string `json:"emailTo"`     // more people to email (a team address)
	Slack       string   `json:"-"`
	Teams       string   `json:"-"`
	Webhook     string   `json:"-"`
	// Sealed holds the three addresses, encrypted.
	Sealed string `json:"sealed"`
}

// Any reports whether there is anywhere to send a notification.
func (n Notifications) Any() bool {
	return n.Enabled && (n.EmailRunner || len(n.EmailTo) > 0 || n.Slack != "" || n.Teams != "" || n.Webhook != "")
}

// Store reads and writes settings.
type Store struct {
	db  *db.DB
	box *Box
}

// New wraps the database. box seals and opens secrets.
func New(d *db.DB, box *Box) *Store { return &Store{db: d, box: box} }

// Box returns the sealing box.
func (s *Store) Box() *Box { return s.box }

// Get loads a section into dst and reports whether it had been saved.
func (s *Store) Get(key string, dst any) (bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE skey = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(v), dst)
}

// Put saves a section.
func (s *Store) Put(key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(s.db.Upsert("settings", "skey", "value", "updated_at"),
		key, string(b), time.Now().UnixMilli())
	return err
}

// Delete removes a section, so the environment defaults apply again.
func (s *Store) Delete(key string) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE skey = ?`, key)
	return err
}

// GetSSO loads the SSO section with its secret opened.
func (s *Store) GetSSO() (SSO, bool, error) {
	var v SSO
	ok, err := s.Get(KeySSO, &v)
	if err != nil || !ok {
		return v, ok, err
	}
	v.ClientSecret, err = s.box.Open(v.SecretSealed)
	return v, true, err
}

// PutSSO saves the SSO section, sealing the secret.
func (s *Store) PutSSO(v SSO) error {
	var err error
	if v.SecretSealed, err = s.box.Seal(v.ClientSecret); err != nil {
		return err
	}
	return s.Put(KeySSO, v)
}

// GetSMTP loads the mail section with its password opened.
func (s *Store) GetSMTP() (SMTP, bool, error) {
	var v SMTP
	ok, err := s.Get(KeySMTP, &v)
	if err != nil || !ok {
		return v, ok, err
	}
	v.Password, err = s.box.Open(v.PasswordSealed)
	return v, true, err
}

// PutSMTP saves the mail section, sealing the password.
func (s *Store) PutSMTP(v SMTP) error {
	var err error
	if v.PasswordSealed, err = s.box.Seal(v.Password); err != nil {
		return err
	}
	return s.Put(KeySMTP, v)
}

// GetCaptcha loads the bot-check section with its secret opened.
func (s *Store) GetCaptcha() (Captcha, bool, error) {
	var v Captcha
	ok, err := s.Get(KeyCaptcha, &v)
	if err != nil || !ok {
		return v, ok, err
	}
	v.Secret, err = s.box.Open(v.SecretSealed)
	return v, true, err
}

// PutCaptcha saves the bot-check section, sealing the secret.
func (s *Store) PutCaptcha(v Captcha) error {
	var err error
	if v.SecretSealed, err = s.box.Seal(v.Secret); err != nil {
		return err
	}
	return s.Put(KeyCaptcha, v)
}

// GetNotifications loads the notification section with its addresses opened.
func (s *Store) GetNotifications() (Notifications, bool, error) {
	var v Notifications
	ok, err := s.Get(KeyNotifications, &v)
	if err != nil || !ok {
		return v, ok, err
	}
	plain, err := s.box.Open(v.Sealed)
	if err != nil {
		return v, true, err
	}
	var hooks struct{ Slack, Teams, Webhook string }
	if plain != "" {
		if err := json.Unmarshal([]byte(plain), &hooks); err != nil {
			return v, true, err
		}
	}
	v.Slack, v.Teams, v.Webhook = hooks.Slack, hooks.Teams, hooks.Webhook
	return v, true, nil
}

// PutNotifications saves the notification section, sealing the webhook addresses.
func (s *Store) PutNotifications(v Notifications) error {
	b, err := json.Marshal(struct{ Slack, Teams, Webhook string }{v.Slack, v.Teams, v.Webhook})
	if err != nil {
		return err
	}
	if v.Sealed, err = s.box.Seal(string(b)); err != nil {
		return err
	}
	return s.Put(KeyNotifications, v)
}
