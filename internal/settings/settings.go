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
	KeyGeneral = "general"
	KeySSO     = "sso"
	KeySMTP    = "smtp"
	KeyGuest   = "guest"
	KeyCaptcha = "captcha"
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
