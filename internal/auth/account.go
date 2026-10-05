package auth

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

// Errors for the account page.
var (
	ErrSameEmail  = errors.New("that is already your email address")
	ErrSSOEmail   = errors.New("your email address comes from your single sign-on provider and is changed there")
	ErrBadName    = errors.New("enter a name between 1 and 80 characters")
	ErrBadImage   = errors.New("choose a PNG, JPEG or WebP picture")
	ErrImageLarge = errors.New("that picture is too large: choose a smaller one")
)

const (
	emailChangeTTL = 24 * time.Hour
	maxAvatarBytes = 150_000
)

// UpdateProfile changes the name shown for a person.
func (s *Service) UpdateProfile(id, name string) (*User, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 {
		return nil, ErrBadName
	}
	return s.store.UpdateUser(id, func(x *User) { x.Name = name })
}

// ChangeEmail moves a person to a new address. They must prove who they are with
// their password. With email set up, the new address first has to be confirmed
// from that address (a link is sent there) and nothing changes until then; without
// email the change applies at once. It reports whether it applied.
func (s *Service) ChangeEmail(base string, u *User, newEmail, password string) (bool, error) {
	newEmail = normEmail(newEmail)
	if !validEmail(newEmail) {
		return false, ErrBadEmail
	}
	if u.PasswordHash == "" {
		return false, ErrSSOEmail
	}
	now := s.now()
	if locked, wait := s.loginEmail.Locked(u.Email, now); locked {
		return false, &ThrottledError{wait}
	}
	if !VerifyPassword(password, u.PasswordHash) {
		s.loginEmail.Fail(u.Email, now)
		return false, ErrInvalidCredentials
	}
	if newEmail == u.Email {
		return false, ErrSameEmail
	}
	if !s.domainAllowed(newEmail) {
		return false, ErrDomain
	}
	if s.store.UserByEmail(newEmail) != nil {
		return false, ErrEmailTaken
	}
	if s.Mailer().Ready() && base != "" {
		token, err := randomToken(32)
		if err != nil {
			return false, err
		}
		if err := s.store.PutEmailChange(tokenHash(token), u.ID, newEmail, now.Add(emailChangeTTL)); err != nil {
			return false, err
		}
		link := strings.TrimRight(base, "/") + "/#/confirm-email?token=" + token
		go func() {
			_ = s.sendThemed([]string{newEmail}, "Confirm your new email for BLASTA", "Confirm your new email address",
				[]string{"Hi " + u.Name + ", you asked to use this address for your BLASTA account. Confirm it to finish the change."},
				"Confirm this address", link, "The link works once and expires in 24 hours. If you did not ask for this, ignore this email: your account stays as it is.")
		}()
		return false, nil
	}
	_, err := s.store.UpdateUser(u.ID, func(x *User) { x.Email = newEmail })
	return err == nil, err
}

// ConfirmEmailChange applies a change of address from the link sent to the new one.
func (s *Service) ConfirmEmailChange(token string) (*User, error) {
	id, newEmail, ok := s.store.TakeEmailChange(tokenHash(token), s.now())
	if !ok {
		return nil, errors.New("this link is invalid or has expired: ask for the change again from your account page")
	}
	if s.store.UserByEmail(newEmail) != nil {
		return nil, ErrEmailTaken
	}
	return s.store.UpdateUser(id, func(x *User) { x.Email = newEmail })
}

// SetAvatar stores a small profile picture, sent as a data: URL. Only raster
// formats are accepted (never SVG, which can carry scripts), and the bytes are
// checked to be what they claim.
func (s *Service) SetAvatar(id, dataURL string) (*User, error) {
	if dataURL == "" {
		return s.store.UpdateUser(id, func(x *User) { x.Avatar = "" })
	}
	var magic []byte
	var prefix string
	for _, f := range []struct {
		prefix string
		magic  []byte
	}{
		{"data:image/jpeg;base64,", []byte{0xFF, 0xD8, 0xFF}},
		{"data:image/png;base64,", []byte{0x89, 'P', 'N', 'G'}},
		{"data:image/webp;base64,", []byte("RIFF")},
	} {
		if strings.HasPrefix(dataURL, f.prefix) {
			prefix, magic = f.prefix, f.magic
		}
	}
	if prefix == "" {
		return nil, ErrBadImage
	}
	if len(dataURL) > maxAvatarBytes {
		return nil, ErrImageLarge
	}
	raw, err := base64.StdEncoding.DecodeString(dataURL[len(prefix):])
	if err != nil || len(raw) < len(magic) || string(raw[:len(magic)]) != string(magic) {
		return nil, ErrBadImage
	}
	return s.store.UpdateUser(id, func(x *User) { x.Avatar = dataURL })
}
