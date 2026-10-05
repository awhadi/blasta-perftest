package auth

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeIdP is a small OpenID Connect provider: discovery, keys, authorize and
// token endpoints, with knobs for producing bad tokens.
type fakeIdP struct {
	srv    *httptest.Server
	rsaKey *rsa.PrivateKey
	ecKey  *ecdsa.PrivateKey
	other  *rsa.PrivateKey // a different key, to forge signatures

	mu    sync.Mutex
	codes map[string]fakeCode
	// what the next token will say
	sub, email, name string
	emailVerified    any
	groups           []string
	aud              any
	issuerOverride   string // changes the discovery document too
	tokenIssuer      string // changes only the ID token's iss claim
	expOffset        time.Duration
	nonceOverride    *string
	signWith         string // "rsa" (default), "ec", "forged", "none", "hs256"
	tokenStatus      int
}

type fakeCode struct{ nonce, challenge, redirect, clientID string }

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	k1, _ := rsa.GenerateKey(rand.Reader, 2048)
	k2, _ := rsa.GenerateKey(rand.Reader, 2048)
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	f := &fakeIdP{rsaKey: k1, other: k2, ecKey: ec, codes: map[string]fakeCode{},
		sub: "idp-user-1", email: "sso@acme.test", name: "Sso User", emailVerified: true, signWith: "rsa"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		iss := f.srv.URL
		if f.issuerOverride != "" {
			iss = f.issuerOverride
		}
		json.NewEncoder(w).Encode(map[string]string{"issuer": iss, "authorization_endpoint": f.srv.URL + "/authorize",
			"token_endpoint": f.srv.URL + "/token", "jwks_uri": f.srv.URL + "/jwks"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		b64 := base64.RawURLEncoding.EncodeToString
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{
			{"kty": "RSA", "kid": "rsa1", "use": "sig", "n": b64(k1.N.Bytes()), "e": b64(big.NewInt(int64(k1.E)).Bytes())},
			{"kty": "EC", "kid": "ec1", "use": "sig", "crv": "P-256", "x": b64(ec.X.Bytes()), "y": b64(ec.Y.Bytes())},
		}})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		code, _ := randomToken(12)
		f.mu.Lock()
		f.codes[code] = fakeCode{q.Get("nonce"), q.Get("code_challenge"), q.Get("redirect_uri"), q.Get("client_id")}
		f.mu.Unlock()
		http.Redirect(w, r, q.Get("redirect_uri")+"?code="+code+"&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		c, ok := f.codes[r.Form.Get("code")]
		delete(f.codes, r.Form.Get("code"))
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != c.challenge || r.Form.Get("redirect_uri") != c.redirect {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		if f.tokenStatus != 0 {
			w.WriteHeader(f.tokenStatus)
			json.NewEncoder(w).Encode(map[string]string{"error": "server_error"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id_token": f.sign(c.nonce, c.clientID), "access_token": "x"})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) sign(nonce, clientID string) string {
	iss := f.srv.URL
	if f.tokenIssuer != "" {
		iss = f.tokenIssuer
	}
	n := nonce
	if f.nonceOverride != nil {
		n = *f.nonceOverride
	}
	aud := f.aud
	if aud == nil {
		aud = clientID
	}
	cl := map[string]any{"iss": iss, "sub": f.sub, "aud": aud, "exp": time.Now().Add(f.expOffset + time.Hour).Unix(),
		"iat": time.Now().Unix(), "nonce": n, "email": f.email, "email_verified": f.emailVerified, "name": f.name}
	if f.groups != nil {
		cl["groups"] = f.groups
	}
	alg, kid := "RS256", "rsa1"
	switch f.signWith {
	case "ec":
		alg, kid = "ES256", "ec1"
	case "none":
		alg = "none"
	case "hs256":
		alg = "HS256"
	}
	hb, _ := json.Marshal(map[string]string{"alg": alg, "kid": kid, "typ": "JWT"})
	pb, _ := json.Marshal(cl)
	e := base64.RawURLEncoding.EncodeToString
	signing := e(hb) + "." + e(pb)
	sum := sha256.Sum256([]byte(signing))
	var sig []byte
	switch f.signWith {
	case "ec":
		r, s, _ := ecdsa.Sign(rand.Reader, f.ecKey, sum[:])
		sig = append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	case "forged":
		sig, _ = rsa.SignPKCS1v15(rand.Reader, f.other, crypto.SHA256, sum[:])
	case "none":
		sig = nil
	case "hs256":
		sig = []byte("whatever")
	default:
		sig, _ = rsa.SignPKCS1v15(rand.Reader, f.rsaKey, crypto.SHA256, sum[:])
	}
	return signing + "." + e(sig)
}

// ssoEnv is BLASTA (the auth service behind its HTTP gate) plus a fake IdP.
type ssoEnv struct {
	idp    *fakeIdP
	svc    *Service
	srv    *httptest.Server
	client *http.Client
}

func newSSOEnv(t *testing.T, mod func(*Config, *OIDCConfig)) *ssoEnv {
	t.Helper()
	idp := newFakeIdP(t)
	oc := &OIDCConfig{Name: "Fake IdP", Issuer: idp.srv.URL, ClientID: "blasta", ClientSecret: "s", AutoCreate: true, AllowInsecure: true}
	cfg := Config{SSO: oc, Registration: RegApproval}
	if mod != nil {
		mod(&cfg, oc)
	}
	var handler http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(srv.Close)
	cfg.PublicURL = srv.URL
	svc := newSvc(t, cfg)
	mux := http.NewServeMux()
	svc.Routes(mux)
	mux.HandleFunc("GET /api/secret", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, ok := svc.Gate(w, r)
		if ok {
			mux.ServeHTTP(w, r)
		}
	})
	jar, _ := cookiejar.New(nil)
	return &ssoEnv{idp: idp, svc: svc, srv: srv, client: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// signIn walks the browser flow: start -> IdP authorize -> callback. It returns
// the callback response (a redirect to the app or to the sign-in error page).
func (e *ssoEnv) signIn(t *testing.T) *http.Response {
	t.Helper()
	resp, err := e.client.Get(e.srv.URL + "/api/auth/oidc/start?next=/%23/templates")
	if err != nil || resp.StatusCode != 302 {
		t.Fatalf("start: %v %v", err, resp)
	}
	resp, err = e.client.Get(resp.Header.Get("Location")) // the IdP
	if err != nil || resp.StatusCode != 302 {
		t.Fatalf("authorize: %v %v", err, resp)
	}
	resp, err = e.client.Get(resp.Header.Get("Location")) // back to BLASTA
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func (e *ssoEnv) isSignedIn(t *testing.T) bool {
	t.Helper()
	resp, err := e.client.Get(e.srv.URL + "/api/secret")
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode == 200
}

func ssoError(resp *http.Response) string {
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "../../../#/login?sso_error=") {
		return ""
	}
	m, _ := url.QueryUnescape(strings.TrimPrefix(loc, "../../../#/login?sso_error="))
	return m
}

func TestSSOHappyPath(t *testing.T) {
	e := newSSOEnv(t, nil)
	if e.isSignedIn(t) {
		t.Fatal("not signed in yet")
	}
	resp := e.signIn(t)
	if resp.StatusCode != 302 || resp.Header.Get("Location") != "../../../#/templates" {
		t.Fatalf("a good sign-in lands on the page that was asked for, got %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if !e.isSignedIn(t) {
		t.Fatal("the session cookie must now work")
	}
	u := e.svc.Store().UserByEmail("sso@acme.test")
	if u == nil || u.Name != "Sso User" || len(u.Identities) != 1 || u.PasswordHash != "" {
		t.Fatalf("the account must be created from the claims, without a password: %+v", u)
	}
	if u.Role != RoleAdmin {
		t.Errorf("the first account ever is the administrator, got %s", u.Role)
	}
	if pc := e.svc.PublicConfig(); !pc.SSO || pc.SSOName != "Fake IdP" {
		t.Errorf("the sign-in page must offer the SSO button: %+v", pc)
	}
}

func TestSSOSecondUserFollowsTrustSetting(t *testing.T) {
	e := newSSOEnv(t, func(c *Config, o *OIDCConfig) { o.Trust = StatusPending })
	e.svc.CreateUser("boss@acme.test", "", goodPW, RoleAdmin) // so the SSO user is not the first
	resp := e.signIn(t)
	if !strings.Contains(ssoError(resp), "waiting for an administrator") {
		t.Fatalf("with trust=pending a new SSO user waits for approval, got %q", resp.Header.Get("Location"))
	}
	if e.isSignedIn(t) {
		t.Error("a pending SSO user must not be signed in")
	}
	u := e.svc.Store().UserByEmail("sso@acme.test")
	if u == nil || u.Status != StatusPending || u.Role != RoleUser {
		t.Errorf("the account is created pending: %+v", u)
	}
}

func TestSSOAdminMapping(t *testing.T) {
	e := newSSOEnv(t, func(c *Config, o *OIDCConfig) { o.AdminGroup = "blasta-admins" })
	e.svc.CreateUser("boss@acme.test", "", goodPW, RoleAdmin)
	e.idp.groups = []string{"engineering", "blasta-admins"}
	e.signIn(t)
	if u := e.svc.Store().UserByEmail("sso@acme.test"); u == nil || u.Role != RoleAdmin {
		t.Errorf("a member of the admin group becomes an administrator: %+v", u)
	}
	e2 := newSSOEnv(t, func(c *Config, o *OIDCConfig) { o.AdminGroup = "blasta-admins" })
	e2.svc.CreateUser("boss@acme.test", "", goodPW, RoleAdmin)
	e2.idp.groups = []string{"engineering"}
	e2.signIn(t)
	if u := e2.svc.Store().UserByEmail("sso@acme.test"); u == nil || u.Role != RoleUser {
		t.Errorf("anyone else is a normal user: %+v", u)
	}
}

func TestSSORefusals(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*fakeIdP)
		cfg  func(*Config, *OIDCConfig)
		want string
	}{
		{"forged signature", func(f *fakeIdP) { f.signWith = "forged" }, nil, "bad signature"},
		{"alg none", func(f *fakeIdP) { f.signWith = "none" }, nil, "unsupported signing algorithm"},
		{"hmac alg confusion", func(f *fakeIdP) { f.signWith = "hs256" }, nil, "unsupported signing algorithm"},
		{"wrong audience", func(f *fakeIdP) { f.aud = "someone-else" }, nil, "not issued for this client"},
		{"several audiences without azp", func(f *fakeIdP) { f.aud = []string{"blasta", "other"} }, nil, "not issued for this client"},
		{"expired token", func(f *fakeIdP) { f.expOffset = -3 * time.Hour }, nil, "expired"},
		{"wrong nonce", func(f *fakeIdP) { n := "replayed"; f.nonceOverride = &n }, nil, "wrong nonce"},
		{"wrong issuer in the token", func(f *fakeIdP) { f.tokenIssuer = "https://evil.test" }, nil, "wrong issuer"},
		{"provider error", func(f *fakeIdP) { f.tokenStatus = 500 }, nil, "refused the sign-in"},
		{"email domain not allowed", nil, func(c *Config, o *OIDCConfig) { c.AllowedDomains = []string{"other.test"} }, "email domains"},
		{"account creation off", nil, func(c *Config, o *OIDCConfig) { o.AutoCreate = false }, "automatic account creation is off"},
		{"no email shared", func(f *fakeIdP) { f.email = "" }, nil, "did not share an email"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newSSOEnv(t, tc.cfg)
			e.svc.CreateUser("boss@acme.test", "", goodPW, RoleAdmin)
			if tc.mod != nil {
				tc.mod(e.idp)
			}
			resp := e.signIn(t)
			msg := ssoError(resp)
			if !strings.Contains(msg, tc.want) {
				t.Fatalf("want an error containing %q, got redirect %q", tc.want, resp.Header.Get("Location"))
			}
			if e.isSignedIn(t) {
				t.Error("a refused sign-in must not create a session")
			}
		})
	}
}

func TestSSOES256Token(t *testing.T) {
	e := newSSOEnv(t, nil)
	e.idp.signWith = "ec"
	if resp := e.signIn(t); resp.StatusCode != 302 || ssoError(resp) != "" || !e.isSignedIn(t) {
		t.Errorf("an ES256-signed token must be accepted: %q", resp.Header.Get("Location"))
	}
}

func TestSSOStateProtections(t *testing.T) {
	// A callback without the state cookie (a login-CSRF attempt) is refused.
	e := newSSOEnv(t, nil)
	resp, _ := e.client.Get(e.srv.URL + "/api/auth/oidc/start")
	idp, _ := e.client.Get(resp.Header.Get("Location"))
	cb := idp.Header.Get("Location")
	jar, _ := cookiejar.New(nil)
	attacker := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r2, _ := attacker.Get(cb) // a different browser, no cookie
	if !strings.Contains(ssoError(r2), "not started from this browser") {
		t.Errorf("a callback from another browser must be refused, got %q", r2.Header.Get("Location"))
	}
	// The state is single use: replaying the real callback fails too.
	if r3, _ := e.client.Get(cb); ssoError(r3) == "" {
		// the first use may legitimately succeed, but a second must not
		r4, _ := e.client.Get(cb)
		if ssoError(r4) == "" {
			t.Error("a replayed callback must be refused")
		}
	}
	// Open redirects are not possible through next.
	for _, bad := range []string{"https://evil.test/", "//evil.test", "/\\evil", "javascript:alert(1)"} {
		if safeNext(bad) != "#/test" {
			t.Errorf("next=%q must fall back to the default", bad)
		}
	}
}

func TestSSOLinkingNeedsVerifiedEmail(t *testing.T) {
	e := newSSOEnv(t, nil)
	e.svc.CreateUser("sso@acme.test", "Local", goodPW, RoleUser) // an existing local account
	e.svc.CreateUser("boss@acme.test", "", goodPW, RoleAdmin)
	e.idp.emailVerified = false
	if resp := e.signIn(t); !strings.Contains(ssoError(resp), "not verified") {
		t.Fatalf("an unverified email must not take over an existing account, got %q", resp.Header.Get("Location"))
	}
	if u := e.svc.Store().UserByEmail("sso@acme.test"); len(u.Identities) != 0 {
		t.Fatal("no identity may be linked")
	}
	e.idp.emailVerified = "true" // some providers send a string
	if resp := e.signIn(t); ssoError(resp) != "" {
		t.Fatalf("a verified email links to the existing account: %q", resp.Header.Get("Location"))
	}
	if u := e.svc.Store().UserByEmail("sso@acme.test"); len(u.Identities) != 1 || u.PasswordHash == "" {
		t.Errorf("the local password must be kept alongside the link: %+v", u)
	}
}

func TestSSODiscoveryChecks(t *testing.T) {
	idp := newFakeIdP(t)
	idp.issuerOverride = "https://not-the-issuer.test" // discovery claims another issuer
	c, err := newOIDCClient(OIDCConfig{Issuer: idp.srv.URL, ClientID: "blasta", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Start(t.Context(), "http://x/cb", "/#/test"); err == nil || !strings.Contains(err.Error(), "issuer") {
		t.Errorf("a discovery document for another issuer must be refused, got %v", err)
	}
	if _, err := newOIDCClient(OIDCConfig{Issuer: "http://idp.example.com", ClientID: "x"}); err == nil {
		t.Error("a plain-http issuer off loopback must be refused unless explicitly allowed")
	}
	if _, err := newOIDCClient(OIDCConfig{Issuer: "http://127.0.0.1:9", ClientID: "x"}); err != nil {
		t.Errorf("a loopback http issuer is fine for development: %v", err)
	}
	if _, err := newOIDCClient(OIDCConfig{Issuer: "", ClientID: "x"}); err == nil {
		t.Error("an issuer is required")
	}
}

// The HTTP surface: who may call what.
func TestGateAndEndpoints(t *testing.T) {
	e := newSSOEnv(t, func(c *Config, o *OIDCConfig) { c.SSO = nil })
	get := func(method, path, body string) (*http.Response, map[string]any) {
		req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := e.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		return resp, m
	}
	if r, _ := get("GET", "/api/secret", ""); r.StatusCode != 401 {
		t.Errorf("protected routes need a session, got %d", r.StatusCode)
	}
	if r, _ := get("GET", "/api/auth/config", ""); r.StatusCode != 200 {
		t.Errorf("config is public, got %d", r.StatusCode)
	}
	if r, _ := get("GET", "/api/auth/me", ""); r.StatusCode != 401 {
		t.Errorf("me without a session is 401, got %d", r.StatusCode)
	}
	if r, _ := get("GET", "/api/auth/oidc/start", ""); r.StatusCode != 404 {
		t.Errorf("SSO endpoints answer 404 when SSO is not configured, got %d", r.StatusCode)
	}
	// First registration signs the admin in.
	r, m := get("POST", "/api/auth/register", `{"email":"root@acme.test","name":"Root","password":"`+goodPW+`"}`)
	if r.StatusCode != 201 || m["role"] != "admin" {
		t.Fatalf("register: %d %v", r.StatusCode, m)
	}
	if r, _ := get("GET", "/api/secret", ""); r.StatusCode != 200 {
		t.Errorf("the first admin is signed in straight away, got %d", r.StatusCode)
	}
	for _, c := range e.client.Jar.Cookies(mustURL(e.srv.URL)) {
		if c.Name == SessionCookie && c.Value == "" {
			t.Error("a session cookie must be set")
		}
	}
	if r, m := get("GET", "/api/auth/me", ""); r.StatusCode != 200 || m["email"] != "root@acme.test" || m["passwordHash"] != nil {
		t.Errorf("me: %d %v (the hash must never be exposed)", r.StatusCode, m)
	}
	// A second person registers and is pending; a normal user cannot use admin routes.
	other := &ssoEnv{srv: e.srv, client: &http.Client{CheckRedirect: e.client.CheckRedirect}}
	other.client.Jar, _ = cookiejar.New(nil)
	post := func(c *http.Client, path, body string) (*http.Response, map[string]any) {
		resp, _ := c.Post(e.srv.URL+path, "application/json", strings.NewReader(body))
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		return resp, m
	}
	r, m = post(other.client, "/api/auth/register", `{"email":"pat@acme.test","password":"`+goodPW+`"}`)
	if r.StatusCode != 201 || m["status"] != "pending" {
		t.Fatalf("a later registration is pending: %d %v", r.StatusCode, m)
	}
	if resp, _ := other.client.Get(e.srv.URL + "/api/secret"); resp.StatusCode != 401 {
		t.Errorf("a pending registration must not be signed in, got %d", resp.StatusCode)
	}
	if r, m := post(other.client, "/api/auth/login", `{"email":"pat@acme.test","password":"`+goodPW+`"}`); r.StatusCode != 403 || m["code"] != "pending" {
		t.Errorf("login says why: %d %v", r.StatusCode, m)
	}
	if r, m := post(other.client, "/api/auth/login", `{"email":"pat@acme.test","password":"nope nope nope"}`); r.StatusCode != 401 || m["code"] != "invalid_credentials" {
		t.Errorf("a wrong password is 401: %d %v", r.StatusCode, m)
	}
	// The admin approves; the user signs in but is still not an admin.
	_, list := get("GET", "/api/admin/users", "")
	var patID string
	for _, u := range list["users"].([]any) {
		if u.(map[string]any)["email"] == "pat@acme.test" {
			patID = u.(map[string]any)["id"].(string)
		}
	}
	if r, _ := get("POST", "/api/admin/users/"+patID+"/status", `{"status":"active"}`); r.StatusCode != 200 {
		t.Fatalf("approve: %d", r.StatusCode)
	}
	if r, _ := post(other.client, "/api/auth/login", `{"email":"pat@acme.test","password":"`+goodPW+`"}`); r.StatusCode != 200 {
		t.Fatalf("approved user signs in: %d", r.StatusCode)
	}
	if resp, _ := other.client.Get(e.srv.URL + "/api/admin/users"); resp.StatusCode != 403 {
		t.Errorf("a normal user must not reach admin routes, got %d", resp.StatusCode)
	}
	// Logout ends the session server-side.
	post(e.client, "/api/auth/logout", "")
	if r, _ := get("GET", "/api/secret", ""); r.StatusCode != 401 {
		t.Errorf("after logout the session is gone, got %d", r.StatusCode)
	}
}

func mustURL(s string) *url.URL { u, _ := url.Parse(s); return u }
