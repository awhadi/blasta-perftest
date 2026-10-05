package auth

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// One-time sign-in codes. Someone who cannot or does not want to type a password
// asks for a code; a six-digit number arrives by email; typing it signs them in.
//
// It is safe because the code is short-lived (10 minutes), single-use, dies after
// five wrong guesses, and asking for codes is throttled per address and per
// network. Only a hash of the code is stored.

const (
	otpTTL         = 10 * time.Minute
	otpMaxAttempts = 5
)

// ErrOTPOff is returned when code sign-in is not available.
var ErrOTPOff = errors.New("signing in with an emailed code is not turned on")

// ErrBadCode is the one answer for every wrong, expired or unknown code.
var ErrBadCode = errors.New("that code is wrong or has expired: ask for a new one")

func otpHash(userID, code string) string { return tokenHash("otp:" + userID + ":" + code) }

// OTPAvailable reports whether code sign-in can be used right now.
func (s *Service) OTPAvailable() bool {
	cfg := s.conf()
	return cfg.OTPLogin && cfg.SMTP.Ready()
}

// RequestOTP emails a sign-in code. It answers the same whether or not the address
// has an account, so it cannot be used to find out who is registered.
func (s *Service) RequestOTP(email, ip string) error {
	if !s.OTPAvailable() {
		return ErrOTPOff
	}
	now := s.now()
	email = normEmail(email)
	for _, k := range []struct {
		t   *Throttle
		key string
	}{{s.otpReq, "otp:" + email}, {s.otpReq, "otpip:" + ip}} {
		if locked, wait := k.t.Locked(k.key, now); locked {
			return &ThrottledError{wait}
		}
		k.t.Fail(k.key, now) // every request counts
	}
	u := s.store.UserByEmail(email)
	if u == nil || (u.Status != StatusActive && u.Status != StatusUnverified) {
		return nil
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return err
	}
	code := fmt.Sprintf("%06d", n.Int64())
	if err := s.store.PutLoginCode(u.ID, otpHash(u.ID, code), now.Add(otpTTL)); err != nil {
		return err
	}
	go func() {
		_ = s.sendThemed([]string{u.Email}, code+" is your BLASTA sign-in code", "Your sign-in code",
			[]string{"Hi " + u.Name + ", use this code to sign in to BLASTA. It works once and expires in 10 minutes.", "  " + spaced(code), "If you did not ask for it, ignore this email: nobody can sign in without the code."},
			"", "", "Never share this code. BLASTA will never ask for it by phone or chat.")
	}()
	return nil
}

func spaced(code string) string { return code[:3] + " " + code[3:] }

// VerifyOTP signs someone in with a code. A person who had not confirmed their
// email yet has just proven the address, so that counts as confirming it.
func (s *Service) VerifyOTP(email, code, ip string) (*User, string, error) {
	if !s.OTPAvailable() {
		return nil, "", ErrOTPOff
	}
	now := s.now()
	email = normEmail(email)
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if locked, wait := s.loginEmail.Locked("otpv:"+email, now); locked {
		return nil, "", &ThrottledError{wait}
	}
	u := s.store.UserByEmail(email)
	if len(code) != 6 || u == nil || !s.store.TryLoginCode(u.ID, otpHash(u.ID, code), now, otpMaxAttempts) {
		s.loginEmail.Fail("otpv:"+email, now)
		return nil, "", ErrBadCode
	}
	s.loginEmail.Reset("otpv:" + email)
	switch u.Status {
	case StatusDisabled:
		return nil, "", ErrDisabled
	case StatusPending:
		return nil, "", ErrPending
	case StatusUnverified:
		status := StatusActive
		if s.conf().Registration == RegApproval {
			status = StatusPending
		}
		var err error
		if u, err = s.store.UpdateUser(u.ID, func(x *User) { x.Status = status }); err != nil {
			return nil, "", err
		}
		if status == StatusPending {
			go s.notifyPending(u)
			return nil, "", ErrPending
		}
	}
	return s.startSession(u)
}

func (s *Service) handleOTPRequest(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Captcha string }
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	if err := s.checkCaptcha(r, ScopeLogin, in.Captcha); err != nil {
		writeErr(w, 400, err.Error(), "captcha")
		return
	}
	err := s.RequestOTP(in.Email, s.clientIP(r))
	var th *ThrottledError
	switch {
	case errors.As(err, &th):
		w.Header().Set("Retry-After", strconv.Itoa(int(th.RetryAfter.Seconds())+1))
		writeErr(w, 429, err.Error(), "throttled")
	case errors.Is(err, ErrOTPOff):
		writeErr(w, 409, err.Error(), "otp_off")
	case err != nil:
		writeErr(w, 400, err.Error())
	default:
		writeJSON(w, 200, map[string]string{"status": "if that address has an account, a code is on its way"})
	}
}

func (s *Service) handleOTPVerify(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Code string }
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	u, token, err := s.VerifyOTP(in.Email, in.Code, s.clientIP(r))
	var th *ThrottledError
	switch {
	case errors.As(err, &th):
		w.Header().Set("Retry-After", strconv.Itoa(int(th.RetryAfter.Seconds())+1))
		writeErr(w, 429, err.Error(), "throttled")
	case errors.Is(err, ErrPending):
		writeErr(w, 403, err.Error(), "pending")
	case errors.Is(err, ErrDisabled):
		writeErr(w, 403, err.Error(), "disabled")
	case errors.Is(err, ErrOTPOff):
		writeErr(w, 409, err.Error(), "otp_off")
	case err != nil:
		writeErr(w, 401, ErrBadCode.Error(), "bad_code")
	default:
		s.setSession(w, r, token)
		writeJSON(w, 200, view(u))
	}
}
