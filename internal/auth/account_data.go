package auth

import (
	"errors"
	"net/http"
	"strings"
)

// ErrSelfDeleteOff is returned when the administrator has turned off self-service deletion.
var ErrSelfDeleteOff = errors.New("this site does not let people delete their own account: ask the administrator")

// SelfDeleteAllowed reports whether people may delete their own account.
func (s *Service) SelfDeleteAllowed() bool { return !s.conf().Privacy.NoSelfDelete }

// ExportAccount is everything the account table side holds about one person, for their own
// download: the account, how they sign in, and their sessions. Secrets (the password hash, session
// tokens) are not included. The run history is added by the caller, which owns it.
func (s *Service) ExportAccount(id string) map[string]any {
	u := s.store.UserByID(id)
	if u == nil {
		return nil
	}
	out := map[string]any{
		"id": u.ID, "email": u.Email, "name": u.Name, "role": u.Role, "status": u.Status,
		"createdAt": u.CreatedAt, "lastLoginAt": u.LastLoginAt,
		"hasPassword": u.PasswordHash != "", "hasPhoto": u.Avatar != "",
	}
	idents := []map[string]string{}
	if rows, err := s.store.db.Query(`SELECT provider, subject FROM identities WHERE user_id = ?`, id); err == nil {
		for rows.Next() {
			var p, sub string
			if rows.Scan(&p, &sub) == nil {
				idents = append(idents, map[string]string{"provider": p, "subject": sub})
			}
		}
		rows.Close()
	}
	out["singleSignOn"] = idents
	sessions := []map[string]any{}
	if rows, err := s.store.db.Query(`SELECT created, last_seen, ip, user_agent FROM sessions WHERE user_id = ?`, id); err == nil {
		for rows.Next() {
			var c, l int64
			var ip, ua string
			if rows.Scan(&c, &l, &ip, &ua) == nil {
				sessions = append(sessions, map[string]any{"started": fromMs(c), "lastUsed": fromMs(l), "networkAddress": ip, "browser": ua})
			}
		}
		rows.Close()
	}
	out["sessions"] = sessions
	return out
}

// DeleteOwnAccount deletes the signed-in person's account after they prove it is them: their
// password, or for an account with none (single sign-on) by typing their email address. Wrong
// attempts are throttled like at sign-in. The last active administrator cannot be deleted.
func (s *Service) DeleteOwnAccount(u *User, password, confirm string) error {
	if !s.SelfDeleteAllowed() {
		return ErrSelfDeleteOff
	}
	now := s.now()
	if locked, wait := s.loginEmail.Locked(u.Email, now); locked {
		return &ThrottledError{wait}
	}
	if u.PasswordHash != "" {
		if !VerifyPassword(password, u.PasswordHash) {
			s.loginEmail.Fail(u.Email, now)
			return errors.New("the password is wrong")
		}
	} else if !strings.EqualFold(strings.TrimSpace(confirm), u.Email) {
		s.loginEmail.Fail(u.Email, now)
		return errors.New("type your email address exactly as shown to confirm")
	}
	s.lg().Info("account deleted by its owner", "user", u.ID)
	return s.Delete(u.ID)
}

// ForgetBrowser drops this browser's sign-in cookie (after the account is gone).
func (s *Service) ForgetBrowser(w http.ResponseWriter, r *http.Request) { s.clearSession(w, r) }
