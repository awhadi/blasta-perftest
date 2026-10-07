package auth

import (
	"errors"

	"github.com/awhadi/blasta-perftest/internal/notify"
)

// EmailOf is a person's email address ("" if there is no such account).
func (s *Service) EmailOf(id string) string {
	if u := s.store.UserByID(id); u != nil && u.Status == StatusActive {
		return u.Email
	}
	return ""
}

// MailReady reports whether outgoing email is set up.
func (s *Service) MailReady() bool { return s.Mailer().Ready() }

// SendRunEmail tells one person that a test has finished.
func (s *Service) SendRunEmail(to string, e notify.Event) error {
	if !s.MailReady() {
		return errors.New("email is not set up on this site")
	}
	paras := make([]string, 0, 6)
	for _, f := range e.Facts() {
		paras = append(paras, f[0]+": "+f[1])
	}
	button := ""
	if e.Link != "" {
		button = "Open the run"
	}
	return s.sendThemed([]string{to}, "BLASTA: "+e.Headline(), e.Headline(), paras, button, e.Link,
		"You get this because you asked to be told when your tests finish. Change it in My account > Notifications.")
}
