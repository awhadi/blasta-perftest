package auth

import "errors"

// ErrNoSealing is returned when this build has no settings store, so nothing can be encrypted.
var ErrNoSealing = errors.New("saved settings are not available here")

// SealText encrypts text with the site's key (the one that protects the SMTP password and other
// stored secrets), for things people save that may hold credentials.
func (s *Service) SealText(plain string) (string, error) {
	if s.settings == nil {
		return "", ErrNoSealing
	}
	return s.settings.Box().Seal(plain)
}

// OpenText decrypts what SealText produced.
func (s *Service) OpenText(sealed string) (string, error) {
	if s.settings == nil {
		return "", ErrNoSealing
	}
	return s.settings.Box().Open(sealed)
}
