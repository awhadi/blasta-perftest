package auth

import (
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/awhadi/blasta-perftest/internal/mailer"
)

const resetTTL = time.Hour

// Mailer returns the outgoing mail settings, which may be unset.
func (s *Service) Mailer() mailer.Config { return s.conf().SMTP }

func (s *Service) siteURL() string {
	return strings.TrimRight(s.conf().PublicURL, "/")
}

// sendThemed delivers a message in BLASTA's look if mail is set up. Mail is a
// convenience on top of the web UI, never required for it, so failures are not
// fatal to the caller.
func (s *Service) sendThemed(to []string, subject, title string, paras []string, button, link, footnote string) error {
	c := s.Mailer()
	if !c.Ready() {
		return errors.New("email is not set up")
	}
	text, html := themed(title, paras, button, link, footnote)
	err := mailer.SendHTML(c, to, subject, text, html)
	s.noteDelivery(to, subject, err)
	return err
}

// MailProblem is the most recent failed delivery, kept for the administrator.
type MailProblem struct {
	At      time.Time `json:"at"`
	To      string    `json:"to"`
	Subject string    `json:"subject"`
	Error   string    `json:"error"`
}

// maskAddr hides most of an address in logs: a***@example.com.
func maskAddr(a string) string {
	i := strings.LastIndex(a, "@")
	if i < 1 {
		return "***"
	}
	return a[:1] + "***" + a[i:]
}

// noteDelivery logs a failed delivery (never the message, its link or its code) and
// remembers it for Settings > Email Delivery; a success clears the note.
func (s *Service) noteDelivery(to []string, subject string, err error) {
	s.mailMu.Lock()
	defer s.mailMu.Unlock()
	if err == nil {
		s.mailProblem = nil
		return
	}
	masked := make([]string, len(to))
	for i, a := range to {
		masked[i] = maskAddr(a)
	}
	s.mailProblem = &MailProblem{At: s.now(), To: strings.Join(masked, ", "), Subject: subject, Error: err.Error()}
	if s.log != nil {
		s.log.Warn("email was not delivered", "to", s.mailProblem.To, "subject", subject, "err", err.Error())
	}
}

// LastMailProblem returns the most recent failed delivery since the last success.
func (s *Service) LastMailProblem() *MailProblem {
	s.mailMu.Lock()
	defer s.mailMu.Unlock()
	if s.mailProblem == nil {
		return nil
	}
	c := *s.mailProblem
	return &c
}

// SetLogger sets where problems are logged.
func (s *Service) SetLogger(l *slog.Logger) { s.log = l }

// notifyPending tells the administrators that someone is waiting for approval.
func (s *Service) notifyPending(u *User) {
	var to []string
	for _, a := range s.store.Users() {
		if a.Role == RoleAdmin && a.Status == StatusActive {
			to = append(to, a.Email)
		}
	}
	if len(to) == 0 {
		return
	}
	link := ""
	if base := s.siteURL(); base != "" {
		link = base + "/#/admin/users"
	}
	_ = s.sendThemed(to, "BLASTA: "+u.Email+" is waiting for approval", "Someone is waiting for approval",
		[]string{u.Name + " (" + u.Email + ") registered for BLASTA and is waiting for an administrator to approve the account."},
		"Review the request", link, "You get this because you are a BLASTA administrator.")
}

// notifyApproved tells a user their account is ready.
func (s *Service) notifyApproved(u *User) {
	link := s.siteURL()
	if link != "" {
		link += "/"
	}
	_ = s.sendThemed([]string{u.Email}, "Your BLASTA account is ready", "Your account is ready",
		[]string{"Hi " + u.Name + ", an administrator approved your BLASTA account. You can sign in now."},
		"Sign in", link, "If you did not ask for a BLASTA account, you can ignore this email.")
}

// ErrUnverified is returned when someone signs in before confirming their email.
var ErrUnverified = errors.New("confirm your email address first: open the link we emailed you")

const confirmTTL = 48 * time.Hour

// sendConfirmation emails a link that proves the address belongs to the person.
func (s *Service) sendConfirmation(base string, u *User) error {
	token, err := randomToken(32)
	if err != nil {
		return err
	}
	if err := s.store.PutConfirmToken(tokenHash(token), u.ID, s.now().Add(confirmTTL)); err != nil {
		return err
	}
	link := strings.TrimRight(base, "/") + "/#/confirm?token=" + token
	go func() {
		_ = s.sendThemed([]string{u.Email}, "Confirm your email for BLASTA", "Confirm your email address",
			[]string{"Hi " + u.Name + ", thanks for signing up for BLASTA. Confirm that this address is yours to finish creating your account."},
			"Confirm my email", link, "The link works once and expires in 48 hours. If you did not sign up, ignore this email and nothing will happen.")
	}()
	return nil
}

// ConfirmEmail uses a confirmation link. The account becomes active, or waits for
// an administrator if approval is required. It returns the user and, if they can
// sign in now, a session token.
func (s *Service) ConfirmEmail(token string) (*User, string, error) {
	id, ok := s.store.TakeConfirmToken(tokenHash(token), s.now())
	if !ok {
		return nil, "", errors.New("this confirmation link is invalid or has expired: sign in to get a new one")
	}
	u := s.store.UserByID(id)
	if u == nil {
		return nil, "", errors.New("this account no longer exists")
	}
	if u.Status != StatusUnverified {
		return u, "", nil // already confirmed (or handled by an administrator)
	}
	status := StatusActive
	if s.conf().Registration == RegApproval {
		status = StatusPending
	}
	u, err := s.store.UpdateUser(id, func(x *User) { x.Status = status })
	if err != nil {
		return nil, "", err
	}
	if status == StatusPending {
		go s.notifyPending(u)
		return u, "", nil
	}
	_, token2, err := s.startSession(u)
	return u, token2, err
}

// ResendConfirmation emails a fresh confirmation link. It answers the same whether
// or not the address needs one, so it cannot be used to find out who is registered.
func (s *Service) ResendConfirmation(base, email, ip string) error {
	if !s.Mailer().Ready() {
		return ErrMailOff
	}
	now := s.now()
	if locked, wait := s.regIP.Locked("confirm:"+ip, now); locked {
		return &ThrottledError{wait}
	}
	s.regIP.Fail("confirm:"+ip, now)
	u := s.store.UserByEmail(email)
	if u == nil || u.Status != StatusUnverified {
		return nil
	}
	return s.sendConfirmation(base, u)
}

// ResendFor lets an administrator resend the link to a specific unverified account.
func (s *Service) ResendFor(base, id string) error {
	u := s.store.UserByID(id)
	if u == nil {
		return errNotFound
	}
	if u.Status != StatusUnverified {
		return errors.New("that account has already confirmed its email")
	}
	if !s.Mailer().Ready() {
		return ErrMailOff
	}
	return s.sendConfirmation(base, u)
}

// ErrResetOff is returned when an administrator has turned password reset off.
var ErrResetOff = errors.New("password reset by email is turned off: ask an administrator")

// ErrMailOff is returned when a feature needs email that is not set up.
var ErrMailOff = errors.New("email is not set up on this BLASTA, so password reset links cannot be sent: ask an administrator")

// RequestReset emails a password-reset link. It answers the same whether or not
// the address has an account, so it cannot be used to find out who is registered.
func (s *Service) RequestReset(email, ip string) error {
	return s.RequestResetFrom(s.siteURL(), email, ip)
}

// RequestResetFrom is RequestReset with the address the link should point at
// (the Public URL, or what the request was addressed to).
func (s *Service) RequestResetFrom(base, email, ip string) error {
	if !s.Mailer().Ready() {
		return ErrMailOff
	}
	if s.conf().DisableReset {
		return ErrResetOff
	}
	now := s.now()
	if locked, wait := s.regIP.Locked("reset:"+ip, now); locked {
		return &ThrottledError{wait}
	}
	s.regIP.Fail("reset:"+ip, now)
	email = normEmail(email)
	u := s.store.UserByEmail(email)
	if u == nil || u.Status != StatusActive {
		return nil
	}
	// Someone who signs in with single sign-on and has no password sets their first one
	// from their account page, signed in: an emailed link must not create one. Tell
	// them how it works instead, so the email they were waiting for does arrive.
	if u.PasswordHash == "" {
		name := "single sign-on"
		if o := s.sso(); o != nil && o.cfg.Name != "" {
			name = o.cfg.Name
		}
		site := strings.TrimRight(base, "/")
		if site != "" {
			site += "/"
		}
		go func() {
			_ = s.sendThemed([]string{u.Email}, "About your BLASTA password", "This account has no password",
				[]string{"Hi " + u.Name + ", someone asked to reset the password for this BLASTA account. This account signs in with " + name + " and has no password, so there is nothing to reset.",
					"Sign in with " + name + ". If you also want to sign in with your email address and a password, open My account after signing in and choose one."},
				"Sign in", site, "If you did not ask for this, ignore this email: nothing changes.")
		}()
		return nil
	}
	token, err := randomToken(32)
	if err != nil {
		return err
	}
	if err := s.store.PutResetToken(tokenHash(token), u.ID, now.Add(resetTTL)); err != nil {
		return err
	}
	if base == "" {
		return errors.New("set the public URL in the administrator settings so reset links can point at BLASTA")
	}
	link := strings.TrimRight(base, "/") + "/#/reset?token=" + token
	go func() {
		_ = s.sendThemed([]string{u.Email}, "Reset your BLASTA password", "Reset your password",
			[]string{"Hi " + u.Name + ", someone asked to reset the password for this BLASTA account. If it was you, choose a new one."},
			"Choose a new password", link, "The link works once and expires in an hour. If it was not you, ignore this email: nothing changes.")
	}()
	return nil
}

// ResetPassword sets a new password from an emailed token, once.
func (s *Service) ResetPassword(token, password string) error {
	id, ok := s.store.TakeResetToken(tokenHash(token), s.now())
	if !ok {
		return errors.New("this reset link is invalid or has expired: ask for a new one")
	}
	if err := s.SetPassword(id, password); err != nil {
		// A weak password should not burn the link: let them try again.
		_ = s.store.PutResetToken(tokenHash(token), id, s.now().Add(resetTTL))
		return err
	}
	return nil
}
