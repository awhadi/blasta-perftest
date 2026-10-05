package auth

import (
	"io"
	"log/slog"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// lg is the logger, or one that drops everything when none was set (tests, the CLI).
func (s *Service) lg() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return discard
}

// who is how a person is named in logs: their id, never their password, session or code.
func who(u *User) string {
	if u == nil {
		return ""
	}
	return u.ID
}
