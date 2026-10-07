package auth

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/awhadi/blasta-perftest/internal/mailer"
	"github.com/awhadi/blasta-perftest/internal/privacy"
	"github.com/awhadi/blasta-perftest/internal/settings"
)

// Registration modes.
const (
	RegOpen     = "open"     // anyone can create an active account
	RegApproval = "approval" // accounts wait for an admin (the default)
	RegClosed   = "closed"   // only admins create accounts (and SSO, if enabled)
)

// Config is the behaviour of the sign-in system.
type Config struct {
	Registration   string
	AllowedDomains []string // empty = any
	// SetupToken, if set, is required to create the very first (admin) account,
	// so nobody who finds a freshly deployed instance can claim it.
	SetupToken  string
	SessionIdle time.Duration
	SessionMax  time.Duration
	// PublicURL is the address users reach BLASTA at, used for SSO redirects.
	PublicURL string
	SSO       *OIDCConfig
	SMTP      mailer.Config
	Guest     settings.Guest
	// TrustedProxies are the reverse proxies (Pangolin, Traefik, nginx...) whose
	// X-Forwarded-For header may be believed. Empty: never believe it.
	TrustedProxies []*net.IPNet
	OTPLogin       bool // sign in with an emailed one-time code
	DisableReset   bool // no "forgot password"
	// Captcha is the bot check; CaptchaOff (BLASTA_CAPTCHA_OFF) switches it off
	// whatever the saved settings say, in case a wrong key locks everyone out.
	Captcha    settings.Captcha
	CaptchaOff bool
	// Analytics is the visitor analytics an administrator switched on (Settings > Analytics).
	Analytics settings.Analytics
	// Privacy is how consent is handled and what the privacy page says (Settings > Privacy & Cookies).
	Privacy settings.Privacy
	// Notifications is how finished tests are announced (Settings > Notifications).
	Notifications settings.Notifications
}

func (c *Config) defaults() {
	if c.Registration == "" {
		c.Registration = RegOpen
	}
	if c.SessionIdle == 0 {
		c.SessionIdle = 12 * time.Hour
	}
	if c.SessionMax == 0 {
		c.SessionMax = 7 * 24 * time.Hour
	}
	if c.Guest == (settings.Guest{}) {
		c.Guest = settings.DefaultGuest()
	}
}

// DefaultSenderName is the sender text on every email until an administrator sets one.
const DefaultSenderName = "BLASTA by AWHADI"

// Errors the HTTP layer turns into responses.
var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrPending            = errors.New("your account is waiting for an administrator to approve it")
	ErrDisabled           = errors.New("this account has been disabled")
	ErrRegistrationClosed = errors.New("registration is closed: ask an administrator to create your account")
	ErrSetupToken         = errors.New("the setup token is missing or wrong")
	ErrDomain             = errors.New("registration is limited to specific email domains, and yours is not one of them")
	ErrBadEmail           = errors.New("enter a valid email address")
	ErrLastAdmin          = errors.New("that would leave BLASTA without an active administrator")
	ErrNotAuthenticated   = errors.New("authentication required")
)

// ThrottledError is returned when too many attempts have failed.
type ThrottledError struct{ RetryAfter time.Duration }

func (e *ThrottledError) Error() string {
	return fmt.Sprintf("too many attempts: try again in %d minutes", int(e.RetryAfter.Minutes())+1)
}

// Service holds the sign-in rules. Create it with New.
type Service struct {
	store *Store
	now   func() time.Time

	loginEmail, loginIP, regIP, otpReq *Throttle
	regMu                              sync.Mutex // makes "is this the first account?" atomic

	// base is what the environment says; cfg and oidc are base with the
	// administrator's saved settings applied, and are swapped by Reload.
	mailMu      sync.Mutex
	mailProblem *MailProblem
	log         *slog.Logger

	mu       sync.RWMutex
	base     Config
	cfg      Config
	oidc     *oidcClient
	settings *settings.Store
}

// conf returns the effective configuration.
func (s *Service) conf() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *Service) sso() *oidcClient {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.oidc
}

// New builds a Service over a store.
func New(store *Store, cfg Config) (*Service, error) {
	cfg.defaults()
	s := &Service{
		store: store, now: time.Now, base: cfg,
		loginEmail: NewThrottle(5, 15*time.Minute),
		loginIP:    NewThrottle(30, 15*time.Minute),
		regIP:      NewThrottle(10, time.Hour),
		otpReq:     NewThrottle(5, 15*time.Minute),
	}
	if err := s.apply(cfg); err != nil {
		return nil, err
	}
	return s, nil
}

// apply validates cfg and makes it the effective configuration.
func (s *Service) apply(cfg Config) error {
	cfg.AllowedDomains = append([]string(nil), cfg.AllowedDomains...)
	if cfg.SMTP.FromName == "" {
		cfg.SMTP.FromName = DefaultSenderName // the same sender text on every email
	}
	switch cfg.Registration {
	case RegOpen, RegApproval, RegClosed:
	default:
		return fmt.Errorf("registration mode %q must be open, approval or closed", cfg.Registration)
	}
	for i, d := range cfg.AllowedDomains {
		cfg.AllowedDomains[i] = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(d), "@"))
	}
	var o *oidcClient
	if cfg.SSO != nil {
		var err error
		if o, err = newOIDCClient(*cfg.SSO); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.cfg, s.oidc = cfg, o
	s.mu.Unlock()
	return nil
}

// UseSettings makes the administrator's saved settings override the
// environment's, and loads them now. Call Reload after every change.
func (s *Service) UseSettings(st *settings.Store) error {
	s.settings = st
	return s.Reload()
}

// Settings returns the settings store, or nil.
func (s *Service) Settings() *settings.Store { return s.settings }

// Reload recomputes the effective configuration: the environment's, with each
// section the administrator has saved taking its place. On error the running
// configuration is left as it was.
func (s *Service) Reload() error {
	cfg, err := s.compose()
	if err != nil {
		return err
	}
	return s.apply(cfg)
}

func (s *Service) compose() (Config, error) {
	cfg := s.base
	cfg.AllowedDomains = append([]string(nil), s.base.AllowedDomains...)
	if s.settings == nil {
		return cfg, nil
	}
	cfg.Notifications = settings.Notifications{On: "problems", EmailRunner: true}
	if v, ok, err := s.settings.GetNotifications(); err != nil {
		return cfg, err
	} else if ok {
		cfg.Notifications = v
	}
	cfg.Privacy = privacy.Default()
	if ok, err := s.settings.Get(settings.KeyPrivacy, &cfg.Privacy); err != nil {
		return cfg, err
	} else if !ok {
		cfg.Privacy = privacy.Default()
	}
	cfg.Analytics = settings.Analytics{RespectDNT: true}
	if ok, err := s.settings.Get(settings.KeyAnalytics, &cfg.Analytics); err != nil {
		return cfg, err
	} else if !ok {
		cfg.Analytics = settings.Analytics{RespectDNT: true}
	}
	var g settings.General
	if ok, err := s.settings.Get(settings.KeyGeneral, &g); err != nil {
		return cfg, err
	} else if ok {
		cfg.PublicURL, cfg.Registration, cfg.AllowedDomains = g.PublicURL, g.Registration, g.AllowedDomains
		cfg.OTPLogin, cfg.DisableReset = g.OTPLogin, g.DisableReset
	}
	if v, ok, err := s.settings.GetSSO(); err != nil {
		return cfg, err
	} else if ok {
		cfg.SSO = nil
		if v.Enabled {
			cfg.SSO = &OIDCConfig{Name: v.Name, Issuer: v.Issuer, DiscoveryURL: v.DiscoveryURL, ClientID: v.ClientID,
				ClientSecret: v.ClientSecret, Scopes: v.Scopes, AllowInsecure: v.AllowInsecure, AutoCreate: v.AutoCreate,
				Trust: v.Trust, AdminEmails: v.AdminEmails, AdminGroup: v.AdminGroup, GroupsClaim: v.GroupsClaim}
		}
	}
	if v, ok, err := s.settings.GetSMTP(); err != nil {
		return cfg, err
	} else if ok {
		cfg.SMTP = mailer.Config{}
		if v.Enabled {
			cfg.SMTP = mailer.Config{Host: v.Host, Port: v.Port, Security: v.Security, Username: v.Username, Password: v.Password, From: v.From, FromName: v.FromName}
		}
	}
	if v, ok, err := s.settings.GetCaptcha(); err != nil {
		return cfg, err
	} else if ok {
		cfg.Captcha = v
	}
	if cfg.CaptchaOff {
		cfg.Captcha.Enabled = false
	}
	var gu settings.Guest
	if ok, err := s.settings.Get(settings.KeyGuest, &gu); err != nil {
		return cfg, err
	} else if ok {
		cfg.Guest = gu
	}
	return cfg, nil
}

// PublicURL is the configured public address, or "".
func (s *Service) PublicURL() string { return s.conf().PublicURL }

// Store exposes the underlying store (for the CLI and tests).
func (s *Service) Store() *Store { return s.store }

// PublicConfig is what the sign-in page needs before anyone is signed in.
type PublicConfig struct {
	NeedsSetup      bool           `json:"needsSetup"`
	Registration    string         `json:"registration"`
	ConfirmEmail    bool           `json:"confirmEmail"` // new accounts must confirm their address by email
	NeedsSetupToken bool           `json:"needsSetupToken"`
	SSO             bool           `json:"sso"`
	SSOName         string         `json:"ssoName,omitempty"`
	AllowedDomains  []string       `json:"allowedDomains,omitempty"`
	Email           bool           `json:"email"` // outgoing mail is set up (password reset works)
	Guest           bool           `json:"guest"` // visitors may try BLASTA without an account
	Captcha         *publicCaptcha `json:"captcha,omitempty"`
	OTP             bool           `json:"otp"`        // sign in with an emailed code
	Reset           bool           `json:"reset"`      // "forgot password" works
	SelfDelete      bool           `json:"selfDelete"` // people may delete their own account
}

// PublicConfig describes the sign-in options.
func (s *Service) PublicConfig() PublicConfig {
	cfg := s.conf()
	pc := PublicConfig{
		NeedsSetup: s.store.Count() == 0, Registration: cfg.Registration,
		AllowedDomains: cfg.AllowedDomains,
	}
	pc.NeedsSetupToken = pc.NeedsSetup && cfg.SetupToken != ""
	if o := s.sso(); o != nil {
		pc.SSO, pc.SSOName = true, o.cfg.Name
	}
	pc.Email = cfg.SMTP.Ready()
	pc.ConfirmEmail = pc.Email && cfg.Registration != RegClosed
	pc.Guest = cfg.Guest.Enabled && !pc.NeedsSetup
	pc.Captcha = s.publicCaptcha()
	pc.OTP = pc.Email && cfg.OTPLogin
	pc.Reset = pc.Email && !cfg.DisableReset
	pc.SelfDelete = !cfg.Privacy.NoSelfDelete
	return pc
}

func (s *Service) domainAllowed(email string) bool {
	allowed := s.conf().AllowedDomains
	if len(allowed) == 0 {
		return true
	}
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	d := email[at+1:]
	for _, a := range allowed {
		if d == a {
			return true
		}
	}
	return false
}

func validEmail(email string) bool {
	if len(email) > 254 {
		return false
	}
	a, err := mail.ParseAddress(email)
	return err == nil && a.Address == email && strings.Contains(email, "@")
}

func newID() string {
	t, _ := randomToken(12)
	return "usr_" + t
}

// Register creates an account. The first account ever becomes an active
// administrator; later ones follow the registration mode.
func (s *Service) Register(email, name, password, setupToken, ip string) (*User, error) {
	return s.RegisterFrom(s.siteURL(), email, name, password, setupToken, ip)
}

// RegisterFrom is Register with the address confirmation links should point at.
//
// With email set up, a new account first has to confirm its address (status
// "unverified"), then becomes active, or waits for an administrator if approval is
// required. Without email there is nothing to confirm with, so the account is
// active at once (or waits for approval, if the administrator asks for that).
func (s *Service) RegisterFrom(base, email, name, password, setupToken, ip string) (*User, error) {
	now := s.now()
	if locked, wait := s.regIP.Locked(ip, now); locked {
		return nil, &ThrottledError{wait}
	}
	s.regIP.Fail(ip, now) // every attempt counts: registration is not something to repeat quickly

	email = normEmail(email)
	if !validEmail(email) {
		return nil, ErrBadEmail
	}
	if err := CheckPassword(password, email); err != nil {
		return nil, err
	}
	if !s.domainAllowed(email) {
		return nil, ErrDomain
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.SplitN(email, "@", 2)[0]
	}
	if len(name) > 80 {
		name = name[:80]
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}

	cfg := s.conf()
	s.regMu.Lock()
	defer s.regMu.Unlock()
	u := &User{ID: newID(), Email: email, Name: name, PasswordHash: hash, CreatedAt: now}
	switch {
	case s.store.Count() == 0:
		if cfg.SetupToken != "" && subtle.ConstantTimeCompare([]byte(setupToken), []byte(cfg.SetupToken)) != 1 {
			return nil, ErrSetupToken
		}
		u.Role, u.Status = RoleAdmin, StatusActive
	case cfg.Registration == RegClosed:
		return nil, ErrRegistrationClosed
	case cfg.SMTP.Ready() && base != "":
		u.Role, u.Status = RoleUser, StatusUnverified
	case cfg.Registration == RegOpen:
		u.Role, u.Status = RoleUser, StatusActive
	default:
		u.Role, u.Status = RoleUser, StatusPending
	}
	if err := s.store.CreateUser(u); err != nil {
		return nil, err
	}
	switch u.Status {
	case StatusUnverified:
		if err := s.sendConfirmation(base, u); err != nil {
			return nil, err
		}
	case StatusPending:
		go s.notifyPending(u)
	}
	return u, nil
}

// Login checks an email and password and starts a session. The error for an
// unknown email and for a wrong password is the same, and takes as long.
func (s *Service) Login(email, password, ip string) (*User, string, error) {
	now := s.now()
	email = normEmail(email)
	for _, k := range []struct {
		t   *Throttle
		key string
	}{{s.loginEmail, email}, {s.loginIP, ip}} {
		if locked, wait := k.t.Locked(k.key, now); locked {
			return nil, "", &ThrottledError{wait}
		}
	}
	u := s.store.UserByEmail(email)
	hash := dummyHash
	if u != nil && u.PasswordHash != "" {
		hash = u.PasswordHash
	}
	ok := VerifyPassword(password, hash)
	if u == nil || u.PasswordHash == "" || !ok {
		s.loginEmail.Fail(email, now)
		s.loginIP.Fail(ip, now)
		return nil, "", ErrInvalidCredentials
	}
	// The password was right, so it is safe to say why the account cannot be used.
	switch u.Status {
	case StatusUnverified:
		return nil, "", ErrUnverified
	case StatusPending:
		return nil, "", ErrPending
	case StatusDisabled:
		return nil, "", ErrDisabled
	}
	s.loginEmail.Reset(email)
	return s.startSession(u)
}

func (s *Service) startSession(u *User) (*User, string, error) {
	now := s.now()
	token, err := randomToken(32)
	if err != nil {
		return nil, "", err
	}
	if err := s.store.PutSession(tokenHash(token), &Session{UserID: u.ID, Created: now, LastSeen: now}); err != nil {
		return nil, "", err
	}
	updated, err := s.store.UpdateUser(u.ID, func(x *User) { x.LastLoginAt = now })
	if err != nil {
		return nil, "", err
	}
	return updated, token, nil
}

// Authenticate resolves a session cookie to an active user.
func (s *Service) Authenticate(token string) (*User, error) {
	if token == "" {
		return nil, ErrNotAuthenticated
	}
	h := tokenHash(token)
	sess := s.store.Session(h)
	now := s.now()
	cfg := s.conf()
	if sess == nil || now.Sub(sess.LastSeen) > cfg.SessionIdle || now.Sub(sess.Created) > cfg.SessionMax {
		if sess != nil {
			s.store.DeleteSession(h)
		}
		return nil, ErrNotAuthenticated
	}
	u := s.store.UserByID(sess.UserID)
	if u == nil || u.Status != StatusActive {
		s.store.DeleteSession(h)
		return nil, ErrNotAuthenticated
	}
	s.store.TouchSession(h, now)
	return u, nil
}

// Logout ends a session.
func (s *Service) Logout(token string) {
	if token != "" {
		s.store.DeleteSession(tokenHash(token))
	}
}

// Prune removes expired sessions; call it now and then.
func (s *Service) Prune() {
	cfg := s.conf()
	s.store.PruneSessions(s.now(), cfg.SessionIdle, cfg.SessionMax)
	s.pruneGuests(s.now())
}

// activeAdmins counts administrators who can sign in, excluding skip.
func (s *Service) activeAdmins(skip string) int {
	n := 0
	for _, u := range s.store.Users() {
		if u.ID != skip && u.Role == RoleAdmin && u.Status == StatusActive {
			n++
		}
	}
	return n
}

// SetStatus approves, disables or re-enables an account.
func (s *Service) SetStatus(id, status string) (*User, error) {
	if status != StatusActive && status != StatusDisabled && status != StatusPending {
		return nil, errors.New("unknown status")
	}
	u := s.store.UserByID(id)
	if u == nil {
		return nil, errNotFound
	}
	if status != StatusActive && u.Role == RoleAdmin && u.Status == StatusActive && s.activeAdmins(id) == 0 {
		return nil, ErrLastAdmin
	}
	out, err := s.store.UpdateUser(id, func(x *User) { x.Status = status })
	if err == nil && status != StatusActive {
		s.store.DeleteUserSessions(id)
	}
	if err == nil && status == StatusActive && u.Status == StatusPending {
		go s.notifyApproved(out)
	}
	return out, err
}

// SetRole promotes or demotes an account.
func (s *Service) SetRole(id, role string) (*User, error) {
	if role != RoleAdmin && role != RoleUser {
		return nil, errors.New("unknown role")
	}
	u := s.store.UserByID(id)
	if u == nil {
		return nil, errNotFound
	}
	if role != RoleAdmin && u.Role == RoleAdmin && u.Status == StatusActive && s.activeAdmins(id) == 0 {
		return nil, ErrLastAdmin
	}
	return s.store.UpdateUser(id, func(x *User) { x.Role = role })
}

// Delete removes an account.
func (s *Service) Delete(id string) error {
	u := s.store.UserByID(id)
	if u == nil {
		return errNotFound
	}
	if u.Role == RoleAdmin && u.Status == StatusActive && s.activeAdmins(id) == 0 {
		return ErrLastAdmin
	}
	return s.store.DeleteUser(id)
}

// SetPassword replaces a password and signs the user out everywhere.
func (s *Service) SetPassword(id, password string) error {
	u := s.store.UserByID(id)
	if u == nil {
		return errNotFound
	}
	if err := CheckPassword(password, u.Email); err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := s.store.UpdateUser(id, func(x *User) { x.PasswordHash = hash }); err != nil {
		return err
	}
	s.store.DeleteUserSessions(id)
	return nil
}

// CreateUser makes an account directly (the CLI and admins), bypassing the
// registration mode.
func (s *Service) CreateUser(email, name, password, role string) (*User, error) {
	email = normEmail(email)
	if !validEmail(email) {
		return nil, ErrBadEmail
	}
	if err := CheckPassword(password, email); err != nil {
		return nil, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	if name = strings.TrimSpace(name); name == "" {
		name = strings.SplitN(email, "@", 2)[0]
	}
	if role != RoleAdmin {
		role = RoleUser
	}
	u := &User{ID: newID(), Email: email, Name: name, Role: role, Status: StatusActive, PasswordHash: hash, CreatedAt: s.now()}
	s.regMu.Lock()
	defer s.regMu.Unlock()
	if err := s.store.CreateUser(u); err != nil {
		return nil, err
	}
	return u, nil
}
