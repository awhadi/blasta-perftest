package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SessionCookie is the name of the session cookie.
const SessionCookie = "blasta_session"
const stateCookie = "blasta_oidc_state"

type ctxKey struct{}

// UserFrom returns the signed-in user a request was authenticated as, if any.
func UserFrom(ctx context.Context) *User {
	u, _ := ctx.Value(ctxKey{}).(*User)
	return u
}

// userView is the safe projection of a user: never the password hash.
type userView struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	Name        string    `json:"name"`
	Role        string    `json:"role"`
	Status      string    `json:"status"`
	Password    bool      `json:"hasPassword"`
	Avatar      string    `json:"avatar,omitempty"`
	SSO         bool      `json:"sso"`
	CreatedAt   time.Time `json:"createdAt"`
	LastLoginAt time.Time `json:"lastLoginAt,omitempty"`
}

func view(u *User) userView {
	return userView{ID: u.ID, Email: u.Email, Name: u.Name, Role: u.Role, Status: u.Status,
		Password: u.PasswordHash != "", Avatar: u.Avatar, SSO: len(u.Identities) > 0, CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string, extra ...string) {
	body := map[string]string{"error": msg}
	if len(extra) > 0 {
		body["code"] = extra[0]
	}
	writeJSON(w, code, body)
}

func decode(r *http.Request, dst any) error { return decodeN(r, dst, 16<<10) }

func decodeN(r *http.Request, dst any, max int64) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, max)).Decode(dst)
}

// clientIP is the address the request came from. X-Forwarded-For is believed
// ONLY when the connection comes from a trusted proxy (BLASTA_TRUSTED_PROXIES):
// otherwise anyone could dodge the sign-in throttle, or the guest limits, by
// changing a header. Behind a trusted proxy the rightmost address that is not
// itself a trusted proxy is the client.
func (s *Service) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	nets := s.conf().TrustedProxies
	trusted := func(ip net.IP) bool {
		for _, n := range nets {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}
	peer := net.ParseIP(host)
	if len(nets) == 0 || peer == nil || !trusted(peer) {
		return host
	}
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := net.ParseIP(strings.TrimSpace(parts[i]))
		if ip == nil {
			break
		}
		if !trusted(ip) {
			return ip.String()
		}
	}
	if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil && !trusted(ip) {
		return ip.String()
	}
	return host
}

func (s *Service) secure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") ||
		strings.HasPrefix(s.conf().PublicURL, "https://")
}

func (s *Service) setSession(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: s.secure(r), MaxAge: int(s.conf().SessionMax.Seconds()),
	})
}

func (s *Service) clearSession(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.secure(r), MaxAge: -1})
}

// publicPaths are reachable without signing in.
func publicPath(p string) bool {
	switch p {
	case "/api/health", "/api/auth/config", "/api/auth/login", "/api/auth/register", "/api/auth/logout",
		"/api/auth/guest", "/api/auth/forgot", "/api/auth/reset", "/api/auth/confirm", "/api/auth/resend", "/api/auth/email/confirm", "/api/auth/guest/captcha", "/api/auth/otp/request", "/api/auth/otp/verify":
		return true
	}
	// The template catalogue is read-only and the same for everyone: visitors may browse it
	// (using a job is a different matter, and needs an account).
	return strings.HasPrefix(p, "/api/auth/oidc/") || p == "/api/presets" || strings.HasPrefix(p, "/api/presets/")
}

// Gate authenticates an API request. It returns the request to serve (carrying
// the user) and whether to continue; when it returns false it has already
// written the response.
func (s *Service) Gate(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	var token string
	if c, err := r.Cookie(SessionCookie); err == nil {
		token = c.Value
	}
	u, err := s.Authenticate(token)
	if err == nil {
		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, u))
	}
	if publicPath(r.URL.Path) || r.URL.Path == "/api/auth/me" {
		return r, true // /me answers for itself
	}
	if err != nil && guestPath(r.URL.Path) && s.conf().Guest.Enabled {
		// A visitor without an account may try BLASTA within the guest limits. Only
		// creating a job starts the trial; everything else just reads.
		create := r.Method == http.MethodPost && r.URL.Path == "/api/jobs"
		g := s.guestFor(w, r, create)
		if g != nil || r.Method == http.MethodGet {
			if g != nil {
				r = r.WithContext(context.WithValue(r.Context(), guestKey{}, g))
			}
			return r, true
		}
	}
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "authentication required", "unauthenticated")
		return r, false
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/") && u.Role != RoleAdmin {
		writeErr(w, http.StatusForbidden, "administrators only", "forbidden")
		return r, false
	}
	return r, true
}

// Routes registers the sign-in and admin endpoints.
func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.PublicConfig())
	})
	mux.HandleFunc("GET /api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		u := UserFrom(r.Context())
		if u == nil {
			writeErr(w, http.StatusUnauthorized, "authentication required", "unauthenticated")
			return
		}
		writeJSON(w, 200, view(u))
	})
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/register", s.handleRegister)
	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(SessionCookie); err == nil {
			s.Logout(c.Value)
		}
		s.clearSession(w, r)
		writeJSON(w, 200, map[string]string{"status": "signed out"})
	})
	mux.HandleFunc("POST /api/auth/password", s.handleChangePassword)
	mux.HandleFunc("GET /api/auth/guest", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.guestStatus(r))
	})
	mux.HandleFunc("POST /api/auth/guest/captcha", s.handleGuestCaptcha)
	mux.HandleFunc("POST /api/auth/otp/request", s.handleOTPRequest)
	mux.HandleFunc("POST /api/auth/otp/verify", s.handleOTPVerify)
	mux.HandleFunc("POST /api/auth/forgot", s.handleForgot)
	mux.HandleFunc("POST /api/auth/reset", s.handleReset)
	mux.HandleFunc("POST /api/auth/confirm", s.handleConfirm)
	mux.HandleFunc("POST /api/auth/resend", s.handleResend)
	s.settingsRoutes(mux)
	s.accountRoutes(mux)
	mux.HandleFunc("GET /api/auth/oidc/start", s.handleOIDCStart)
	mux.HandleFunc("GET /api/auth/oidc/callback", s.handleOIDCCallback)

	mux.HandleFunc("GET /api/admin/users", func(w http.ResponseWriter, r *http.Request) {
		out := []userView{}
		for _, u := range s.store.Users() {
			out = append(out, view(u))
		}
		writeJSON(w, 200, map[string]any{"users": out})
	})
	mux.HandleFunc("POST /api/admin/users", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Email, Name, Password, Role string }
		if err := decode(r, &in); err != nil {
			writeErr(w, 400, "invalid request")
			return
		}
		u, err := s.CreateUser(in.Email, in.Name, in.Password, in.Role)
		if errors.Is(err, ErrEmailTaken) {
			writeErr(w, 409, err.Error(), "exists")
			return
		}
		if err != nil {
			s.adminError(w, err)
			return
		}
		s.lg().Info("account created by an administrator", "user", who(u), "role", u.Role, "by", who(UserFrom(r.Context())))
		writeJSON(w, 201, view(u))
	})
	mux.HandleFunc("POST /api/admin/users/{id}/resend", func(w http.ResponseWriter, r *http.Request) {
		if err := s.ResendFor(s.siteBase(r), r.PathValue("id")); err != nil {
			if errors.Is(err, ErrMailOff) {
				writeErr(w, 409, err.Error(), "mail_off")
				return
			}
			s.adminError(w, err)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "sent"})
	})
	mux.HandleFunc("POST /api/admin/users/{id}/status", s.adminAction(func(id string, body map[string]string) (*User, error) {
		return s.SetStatus(id, body["status"])
	}))
	mux.HandleFunc("POST /api/admin/users/{id}/role", s.adminAction(func(id string, body map[string]string) (*User, error) {
		return s.SetRole(id, body["role"])
	}))
	mux.HandleFunc("POST /api/admin/users/{id}/password", s.adminAction(func(id string, body map[string]string) (*User, error) {
		if err := s.SetPassword(id, body["password"]); err != nil {
			return nil, err
		}
		return s.store.UserByID(id), nil
	}))
	mux.HandleFunc("DELETE /api/admin/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Delete(r.PathValue("id")); err != nil {
			s.adminError(w, err)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "deleted"})
	})
}

func (s *Service) adminAction(fn func(id string, body map[string]string) (*User, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := decode(r, &body); err != nil {
			writeErr(w, 400, "invalid request")
			return
		}
		u, err := fn(r.PathValue("id"), body)
		if err != nil {
			s.adminError(w, err)
			return
		}
		writeJSON(w, 200, view(u))
	}
}

func (s *Service) adminError(w http.ResponseWriter, err error) {
	var weak WeakPasswordError
	switch {
	case errors.Is(err, errNotFound):
		writeErr(w, 404, "no such user")
	case errors.Is(err, ErrLastAdmin):
		writeErr(w, 409, err.Error(), "last_admin")
	case errors.As(err, &weak):
		writeErr(w, 400, err.Error(), "weak_password")
	default:
		writeErr(w, 400, err.Error())
	}
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password, Captcha string }
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	if err := s.checkCaptcha(r, ScopeLogin, in.Captcha); err != nil {
		writeErr(w, 400, err.Error(), "captcha")
		return
	}
	u, token, err := s.Login(in.Email, in.Password, s.clientIP(r))
	if err != nil {
		var th *ThrottledError
		if errors.As(err, &th) {
			s.lg().Warn("sign-in throttled", "email", maskAddr(in.Email), "ip", s.clientIP(r), "retryAfter", th.RetryAfter.Round(time.Second).String())
		} else {
			s.lg().Info("sign-in refused", "email", maskAddr(in.Email), "ip", s.clientIP(r), "reason", err.Error())
		}
		switch {
		case errors.As(err, &th):
			w.Header().Set("Retry-After", strconv.Itoa(int(th.RetryAfter.Seconds())+1))
			writeErr(w, 429, err.Error(), "throttled")
		case errors.Is(err, ErrUnverified):
			writeErr(w, 403, err.Error(), "unverified")
		case errors.Is(err, ErrPending):
			writeErr(w, 403, err.Error(), "pending")
		case errors.Is(err, ErrDisabled):
			writeErr(w, 403, err.Error(), "disabled")
		default:
			writeErr(w, 401, ErrInvalidCredentials.Error(), "invalid_credentials")
		}
		return
	}
	s.setSession(w, r, token)
	s.lg().Info("signed in", "user", who(u), "ip", s.clientIP(r))
	writeJSON(w, 200, view(u))
}

func (s *Service) handleRegister(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Name, Password, SetupToken, Captcha string }
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	if s.store.Count() > 0 { // the very first account is the administrator: no check, or nobody could set up
		if err := s.checkCaptcha(r, ScopeRegister, in.Captcha); err != nil {
			writeErr(w, 400, err.Error(), "captcha")
			return
		}
	}
	u, err := s.RegisterFrom(s.siteBase(r), in.Email, in.Name, in.Password, in.SetupToken, s.clientIP(r))
	if err != nil {
		s.lg().Info("registration refused", "email", maskAddr(in.Email), "ip", s.clientIP(r), "reason", err.Error())
		var th *ThrottledError
		var weak WeakPasswordError
		switch {
		case errors.As(err, &th):
			w.Header().Set("Retry-After", strconv.Itoa(int(th.RetryAfter.Seconds())+1))
			writeErr(w, 429, err.Error(), "throttled")
		case errors.As(err, &weak):
			writeErr(w, 400, err.Error(), "weak_password")
		case errors.Is(err, ErrEmailTaken):
			writeErr(w, 409, err.Error(), "exists")
		case errors.Is(err, ErrRegistrationClosed), errors.Is(err, ErrDomain), errors.Is(err, ErrSetupToken):
			writeErr(w, 403, err.Error(), "not_allowed")
		default:
			writeErr(w, 400, err.Error())
		}
		return
	}
	// An approved account (the first admin, or open registration) is signed in at
	// once; a pending one is told to wait.
	if u.Status == StatusActive {
		_, token, err := s.startSession(u)
		if err == nil {
			s.setSession(w, r, token)
		}
	}
	s.lg().Info("account created", "user", who(u), "role", u.Role, "status", u.Status, "ip", s.clientIP(r))
	writeJSON(w, 201, view(u))
}

func (s *Service) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	if u == nil {
		writeErr(w, 401, "authentication required", "unauthenticated")
		return
	}
	var in struct{ Current, New string }
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	// An account made through single sign-on has no password until its owner sets one
	// here, signed in: that first password needs no "current" one. Once there is one,
	// changing it needs the old one, and wrong guesses are throttled like at sign-in.
	if u.PasswordHash != "" {
		now := s.now()
		if locked, wait := s.loginEmail.Locked(u.Email, now); locked {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			writeErr(w, 429, (&ThrottledError{wait}).Error(), "throttled")
			return
		}
		if !VerifyPassword(in.Current, u.PasswordHash) {
			s.loginEmail.Fail(u.Email, now)
			s.lg().Warn("password change refused: wrong current password", "user", who(u), "ip", s.clientIP(r))
			writeErr(w, 403, "the current password is wrong", "invalid_credentials")
			return
		}
	}
	if err := s.SetPassword(u.ID, in.New); err != nil {
		s.adminError(w, err)
		return
	}
	// SetPassword signed everyone out, this browser included: start a fresh session.
	fresh := s.store.UserByID(u.ID)
	if _, token, err := s.startSession(fresh); err == nil {
		s.setSession(w, r, token)
	}
	s.lg().Info("password changed", "user", who(u), "ip", s.clientIP(r))
	writeJSON(w, 200, map[string]string{"status": "password changed", "hadPassword": strconv.FormatBool(u.PasswordHash != "")})
}

// ---- SSO -------------------------------------------------------------------

// forwardedPrefix is the path a reverse proxy mounted BLASTA under and stripped
// (Traefik's StripPrefix sends it as X-Forwarded-Prefix). Only plain path
// characters are accepted, since it ends up in addresses we hand out.
func forwardedPrefix(r *http.Request) string { return CleanBase(r.Header.Get("X-Forwarded-Prefix")) }

// CleanBase normalises a URL path prefix: "" or "/blasta", never a trailing
// slash, only characters that are safe in a path.
func CleanBase(p string) string {
	p = strings.TrimSpace(p)
	if strings.Contains(p, "//") {
		return ""
	}
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	for _, c := range p {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~/", c)) {
			return ""
		}
	}
	if strings.Contains(p, "//") || strings.Contains(p, "..") {
		return ""
	}
	return "/" + p
}

// PublicPath is the path part of the Public URL ("/blasta" for
// https://example.com/blasta), which is where BLASTA is mounted.
func (s *Service) PublicPath() string {
	u, err := url.Parse(s.conf().PublicURL)
	if err != nil {
		return ""
	}
	return CleanBase(u.Path)
}

// siteBase is where BLASTA is reached from outside: the Public URL, or else what
// this request was addressed to (scheme, host and any prefix a proxy reported).
func (s *Service) siteBase(r *http.Request) string {
	if base := strings.TrimRight(s.conf().PublicURL, "/"); base != "" {
		return base
	}
	scheme := "http"
	if s.secure(r) {
		scheme = "https"
	}
	host := r.Host
	if fh := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0]); fh != "" {
		host = fh
	}
	return scheme + "://" + host + forwardedPrefix(r)
}

// SiteBase is where BLASTA is reached from outside (see siteBase), for absolute links in
// pages meant for crawlers.
func (s *Service) SiteBase(r *http.Request) string { return s.siteBase(r) }

// redirectURL is the address the identity provider sends people back to: the
// Public URL if one is set, otherwise what this request was addressed to.
func (s *Service) redirectURL(r *http.Request) string {
	return s.siteBase(r) + "/api/auth/oidc/callback"
}

// RedirectURL is exported for the settings page.
func (s *Service) RedirectURL(r *http.Request) string { return s.redirectURL(r) }

// safeNext only allows a hash route on this site, never an absolute URL.
func safeNext(next string) string {
	next = strings.TrimPrefix(next, "/")
	if strings.HasPrefix(next, "#/") && !strings.ContainsAny(next, "\\\r\n") {
		return next
	}
	return "#/test"
}

// backHome is a relative address that leads from the callback
// (<mount>/api/auth/oidc/callback) to the app's own page, wherever BLASTA is
// mounted: directly, under a path, or behind a proxy that strips the path.
// relativeRedirect sends a 302 whose Location is kept relative. (http.Redirect
// would resolve it against the request path, which loses a mount prefix that
// only the browser knows about.)
func relativeRedirect(w http.ResponseWriter, loc string) {
	w.Header().Set("Location", loc)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusFound)
}

func backHome(route string) string { return "../../../" + route }

func (s *Service) ssoFail(w http.ResponseWriter, r *http.Request, msg string) {
	relativeRedirect(w, backHome("#/login?sso_error="+url.QueryEscape(msg)))
}

func (s *Service) handleOIDCStart(w http.ResponseWriter, r *http.Request) {
	oidc := s.sso()
	if oidc == nil {
		writeErr(w, 404, "single sign-on is not configured")
		return
	}
	authURL, state, err := oidc.Start(r.Context(), s.redirectURL(r), safeNext(r.URL.Query().Get("next")))
	s.lg().Debug("single sign-on started", "redirect", s.redirectURL(r), "ip", s.clientIP(r))
	if err != nil {
		s.lg().Warn("single sign-on could not start", "err", err.Error())
		s.ssoFail(w, r, err.Error())
		return
	}
	// The state cookie ties the callback to THIS browser (login-CSRF protection).
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Value: state, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.secure(r), MaxAge: 600})
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *Service) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	oidc := s.sso()
	if oidc == nil {
		writeErr(w, 404, "single sign-on is not configured")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Value: "", Path: "/", MaxAge: -1})
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		s.lg().Warn("single sign-on: the provider returned an error", "error", e, "description", q.Get("error_description"))
		s.ssoFail(w, r, "the provider returned an error: "+e+" "+q.Get("error_description"))
		return
	}
	var cookieState string
	if c, err := r.Cookie(stateCookie); err == nil {
		cookieState = c.Value
	}
	claims, next, err := oidc.Finish(r.Context(), s.redirectURL(r), q.Get("state"), cookieState, q.Get("code"))
	if err != nil {
		s.lg().Warn("single sign-on failed", "stage", "token exchange or validation", "err", err.Error(), "ip", s.clientIP(r))
		s.ssoFail(w, r, err.Error())
		return
	}
	u, token, err := s.LoginSSO(claims)
	if err != nil {
		s.lg().Warn("single sign-on refused", "email", maskAddr(claims.Email), "err", err.Error(), "ip", s.clientIP(r))
		s.ssoFail(w, r, err.Error())
		return
	}
	s.lg().Info("signed in with single sign-on", "user", who(u), "ip", s.clientIP(r))
	s.setSession(w, r, token)
	relativeRedirect(w, backHome(next))
}

func (s *Service) handleForgot(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Captcha string }
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	if err := s.checkCaptcha(r, ScopeLogin, in.Captcha); err != nil {
		writeErr(w, 400, err.Error(), "captcha")
		return
	}
	err := s.RequestResetFrom(s.siteBase(r), in.Email, s.clientIP(r))
	s.lg().Info("password reset requested", "email", maskAddr(in.Email), "ip", s.clientIP(r), "ok", err == nil)
	var th *ThrottledError
	switch {
	case errors.As(err, &th):
		w.Header().Set("Retry-After", strconv.Itoa(int(th.RetryAfter.Seconds())+1))
		writeErr(w, 429, err.Error(), "throttled")
	case errors.Is(err, ErrMailOff):
		writeErr(w, 409, err.Error(), "mail_off")
	case errors.Is(err, ErrResetOff):
		writeErr(w, 409, err.Error(), "reset_off")
	case err != nil:
		writeErr(w, 400, err.Error())
	default:
		writeJSON(w, 200, map[string]string{"status": "if that address has an account, a reset link is on its way"})
	}
}

func (s *Service) handleReset(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token, Password string }
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	if err := s.ResetPassword(in.Token, in.Password); err != nil {
		s.lg().Info("password reset refused", "ip", s.clientIP(r), "reason", err.Error())
		s.adminError(w, err)
		return
	}
	s.lg().Info("password reset completed", "ip", s.clientIP(r))
	writeJSON(w, 200, map[string]string{"status": "password changed"})
}

func (s *Service) handleConfirm(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token string }
	if err := decode(r, &in); err != nil || in.Token == "" {
		writeErr(w, 400, "invalid request")
		return
	}
	u, token, err := s.ConfirmEmail(in.Token)
	if err != nil {
		s.lg().Info("email confirmation refused", "ip", s.clientIP(r), "reason", err.Error())
		writeErr(w, 400, err.Error(), "invalid_token")
		return
	}
	s.lg().Info("email address confirmed", "user", who(u), "ip", s.clientIP(r))
	if token != "" {
		s.setSession(w, r, token)
	}
	writeJSON(w, 200, view(u))
}

func (s *Service) handleResend(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Captcha string }
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	if err := s.checkCaptcha(r, ScopeLogin, in.Captcha); err != nil {
		writeErr(w, 400, err.Error(), "captcha")
		return
	}
	err := s.ResendConfirmation(s.siteBase(r), in.Email, s.clientIP(r))
	var th *ThrottledError
	switch {
	case errors.As(err, &th):
		w.Header().Set("Retry-After", strconv.Itoa(int(th.RetryAfter.Seconds())+1))
		writeErr(w, 429, err.Error(), "throttled")
	case errors.Is(err, ErrMailOff):
		writeErr(w, 409, err.Error(), "mail_off")
	case err != nil:
		writeErr(w, 400, err.Error())
	default:
		writeJSON(w, 200, map[string]string{"status": "if that address is waiting for confirmation, a new link is on its way"})
	}
}

// handleGuestCaptcha lets a visitor pass the bot check once, before their first
// free test. It starts their trial record if they have none.
func (s *Service) handleGuestCaptcha(w http.ResponseWriter, r *http.Request) {
	var in struct{ Captcha string }
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	g := s.guestFor(w, r, true)
	if g == nil {
		writeErr(w, 404, "the free trial is not available")
		return
	}
	if err := s.checkCaptcha(r, ScopeGuest, in.Captcha); err != nil {
		writeErr(w, 400, err.Error(), "captcha")
		return
	}
	_, _ = s.store.db.Exec(`UPDATE guests SET verified = 1 WHERE id = ?`, g.ID)
	writeJSON(w, 200, map[string]string{"status": "verified"})
}
