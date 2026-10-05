package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OIDCConfig configures single sign-on with any OpenID Connect provider
// (Keycloak, Okta, Entra ID, Auth0, Google, authentik, ...).
type OIDCConfig struct {
	Name         string // shown on the button, for example "Keycloak"
	Issuer       string // the issuer identifier, exactly as it appears in tokens
	DiscoveryURL string // optional: fetch the discovery document from here instead (internal address)
	ClientID     string
	ClientSecret string
	Scopes       string // default "openid email profile"
	// AllowInsecure permits an http:// issuer that is not on loopback. Development only.
	AllowInsecure bool
	// AutoCreate creates an account on the first SSO sign-in. Trust is the status
	// given to it: "active" (default; the provider vouches for the person) or
	// "pending" (an admin must still approve).
	AutoCreate  bool
	Trust       string
	AdminEmails []string // these emails become administrators
	AdminGroup  string   // members of this group become administrators
	GroupsClaim string   // default "groups"
}

// Claims is what BLASTA takes from a verified ID token.
type Claims struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Groups        []string
}

type discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

type oidcState struct {
	nonce, verifier, next string
	expires               time.Time
}

type oidcClient struct {
	cfg OIDCConfig
	hc  *http.Client
	now func() time.Time

	mu      sync.Mutex
	disc    *discovery
	discAt  time.Time
	keys    map[string]crypto.PublicKey
	keysAt  time.Time
	pending map[string]*oidcState
}

func isLoopbackHost(h string) bool {
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func checkEndpointURL(raw string, allowInsecure bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%q is not a valid URL", raw)
	}
	if u.Scheme == "https" || allowInsecure {
		return nil
	}
	if u.Scheme == "http" && isLoopbackHost(u.Hostname()) {
		return nil
	}
	return fmt.Errorf("%q must use https (set the insecure option only for development)", raw)
}

func newOIDCClient(cfg OIDCConfig) (*oidcClient, error) {
	cfg.Issuer = strings.TrimRight(strings.TrimSpace(cfg.Issuer), "/")
	if cfg.Issuer == "" || cfg.ClientID == "" {
		return nil, errors.New("single sign-on needs an issuer and a client id")
	}
	if err := checkEndpointURL(cfg.Issuer, cfg.AllowInsecure); err != nil {
		return nil, err
	}
	if cfg.DiscoveryURL != "" {
		if err := checkEndpointURL(cfg.DiscoveryURL, cfg.AllowInsecure); err != nil {
			return nil, err
		}
	}
	if cfg.Name == "" {
		cfg.Name = "single sign-on"
	}
	if cfg.Scopes == "" {
		cfg.Scopes = "openid email profile"
	}
	if cfg.GroupsClaim == "" {
		cfg.GroupsClaim = "groups"
	}
	if cfg.Trust == "" {
		cfg.Trust = StatusActive
	}
	if cfg.Trust != StatusActive && cfg.Trust != StatusPending {
		return nil, errors.New("sso trust must be active or pending")
	}
	return &oidcClient{
		cfg: cfg, now: time.Now, pending: map[string]*oidcState{},
		hc: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (c *oidcClient) getJSON(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s answered %d", rawURL, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

// discover returns the provider's endpoints, cached for an hour. The issuer the
// document claims must match the configured one (OIDC mix-up protection).
func (c *oidcClient) discover(ctx context.Context) (*discovery, error) {
	c.mu.Lock()
	if c.disc != nil && c.now().Sub(c.discAt) < time.Hour {
		d := c.disc
		c.mu.Unlock()
		return d, nil
	}
	c.mu.Unlock()
	src := c.cfg.DiscoveryURL
	if src == "" {
		src = c.cfg.Issuer + "/.well-known/openid-configuration"
	}
	var d discovery
	if err := c.getJSON(ctx, src, &d); err != nil {
		return nil, fmt.Errorf("could not read the provider's discovery document: %w", err)
	}
	if strings.TrimRight(d.Issuer, "/") != c.cfg.Issuer {
		return nil, fmt.Errorf("the provider reports issuer %q, not the configured %q", d.Issuer, c.cfg.Issuer)
	}
	for _, e := range []string{d.AuthorizationEndpoint, d.TokenEndpoint, d.JWKSURI} {
		if err := checkEndpointURL(e, c.cfg.AllowInsecure); err != nil {
			return nil, err
		}
	}
	c.mu.Lock()
	c.disc, c.discAt = &d, c.now()
	c.mu.Unlock()
	return &d, nil
}

// Start returns the provider URL to send the browser to, and the state that must
// come back. next is where to land after signing in (a local path only).
func (c *oidcClient) Start(ctx context.Context, redirectURL, next string) (authURL, state string, err error) {
	d, err := c.discover(ctx)
	if err != nil {
		return "", "", err
	}
	state, _ = randomToken(24)
	nonce, _ := randomToken(24)
	verifier, _ := randomToken(32)
	sum := sha256.Sum256([]byte(verifier))
	c.mu.Lock()
	now := c.now()
	for k, v := range c.pending { // prune, and bound the table
		if now.After(v.expires) {
			delete(c.pending, k)
		}
	}
	if len(c.pending) > 1000 {
		c.mu.Unlock()
		return "", "", errors.New("too many sign-ins in progress")
	}
	c.pending[state] = &oidcState{nonce: nonce, verifier: verifier, next: next, expires: now.Add(10 * time.Minute)}
	c.mu.Unlock()

	q := url.Values{
		"response_type": {"code"}, "client_id": {c.cfg.ClientID}, "redirect_uri": {redirectURL},
		"scope": {c.cfg.Scopes}, "state": {state}, "nonce": {nonce},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
	}
	sep := "?"
	if strings.Contains(d.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return d.AuthorizationEndpoint + sep + q.Encode(), state, nil
}

// Finish completes the sign-in: it checks the state, exchanges the code, and
// verifies the ID token. cookieState is the state the browser stored when it left.
func (c *oidcClient) Finish(ctx context.Context, redirectURL, state, cookieState, code string) (*Claims, string, error) {
	if state == "" || cookieState == "" || state != cookieState {
		return nil, "", errors.New("the sign-in was not started from this browser: try again")
	}
	c.mu.Lock()
	st := c.pending[state]
	delete(c.pending, state) // single use
	c.mu.Unlock()
	if st == nil || c.now().After(st.expires) {
		return nil, "", errors.New("the sign-in expired: try again")
	}
	d, err := c.discover(ctx)
	if err != nil {
		return nil, "", err
	}
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURL},
		"client_id": {c.cfg.ClientID}, "code_verifier": {st.verifier},
	}
	if c.cfg.ClientSecret != "" {
		form.Set("client_secret", c.cfg.ClientSecret)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("could not reach the provider's token endpoint: %w", err)
	}
	defer resp.Body.Close()
	var tok struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
		Desc    string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tok); err != nil {
		return nil, "", errors.New("the provider's token response was not understood")
	}
	if resp.StatusCode != 200 || tok.IDToken == "" {
		return nil, "", fmt.Errorf("the provider refused the sign-in: %s %s", tok.Error, tok.Desc)
	}
	claims, err := c.verifyIDToken(ctx, tok.IDToken, st.nonce)
	if err != nil {
		return nil, "", fmt.Errorf("the ID token was rejected: %w", err)
	}
	return claims, st.next, nil
}

// ---- ID token verification --------------------------------------------------

type jwk struct {
	Kty, Kid, Use, Crv, N, E, X, Y string
}

func b64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

func (k jwk) publicKey() (crypto.PublicKey, error) {
	switch k.Kty {
	case "RSA":
		n, e := new(big.Int), new(big.Int)
		nb, err1 := b64(k.N)
		eb, err2 := b64(k.E)
		if err1 != nil || err2 != nil {
			return nil, errors.New("bad RSA key")
		}
		n.SetBytes(nb)
		e.SetBytes(eb)
		if !e.IsInt64() || n.BitLen() < 2048 {
			return nil, errors.New("RSA key too small")
		}
		return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
	case "EC":
		if k.Crv != "P-256" {
			return nil, errors.New("unsupported curve")
		}
		xb, err1 := b64(k.X)
		yb, err2 := b64(k.Y)
		if err1 != nil || err2 != nil {
			return nil, errors.New("bad EC key")
		}
		pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(xb), Y: new(big.Int).SetBytes(yb)}
		if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
			return nil, errors.New("EC key is not on the curve")
		}
		return pub, nil
	}
	return nil, errors.New("unsupported key type")
}

// signingKey finds the key for kid, refetching the key set once if it is not
// known (providers rotate keys).
func (c *oidcClient) signingKey(ctx context.Context, kid string, refresh bool) (crypto.PublicKey, error) {
	c.mu.Lock()
	k, ok := c.keys[kid]
	fresh := c.now().Sub(c.keysAt) < 10*time.Minute
	c.mu.Unlock()
	if ok && fresh {
		return k, nil
	}
	if !ok || !fresh {
		d, err := c.discover(ctx)
		if err != nil {
			return nil, err
		}
		var set struct{ Keys []jwk }
		if err := c.getJSON(ctx, d.JWKSURI, &set); err != nil {
			return nil, fmt.Errorf("could not read the provider's signing keys: %w", err)
		}
		keys := map[string]crypto.PublicKey{}
		for _, jk := range set.Keys {
			if jk.Use != "" && jk.Use != "sig" {
				continue
			}
			if pk, err := jk.publicKey(); err == nil {
				keys[jk.Kid] = pk
			}
		}
		c.mu.Lock()
		c.keys, c.keysAt = keys, c.now()
		k, ok = keys[kid]
		c.mu.Unlock()
		if ok {
			return k, nil
		}
	}
	return nil, errors.New("the token is signed with an unknown key")
}

func (c *oidcClient) verifyIDToken(ctx context.Context, raw, wantNonce string) (*Claims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("not a JWT")
	}
	hb, err1 := b64(parts[0])
	pb, err2 := b64(parts[1])
	sig, err3 := b64(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return nil, errors.New("malformed JWT")
	}
	var h struct{ Alg, Kid string }
	if json.Unmarshal(hb, &h) != nil {
		return nil, errors.New("malformed JWT header")
	}
	// Only asymmetric algorithms: "none" and HMAC (where the secret would be the
	// public key) are refused outright.
	if h.Alg != "RS256" && h.Alg != "ES256" {
		return nil, fmt.Errorf("unsupported signing algorithm %q", h.Alg)
	}
	key, err := c.signingKey(ctx, h.Kid, true)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	switch pk := key.(type) {
	case *rsa.PublicKey:
		if h.Alg != "RS256" || rsa.VerifyPKCS1v15(pk, crypto.SHA256, sum[:], sig) != nil {
			return nil, errors.New("bad signature")
		}
	case *ecdsa.PublicKey:
		if h.Alg != "ES256" || len(sig) != 64 ||
			!ecdsa.Verify(pk, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
			return nil, errors.New("bad signature")
		}
	default:
		return nil, errors.New("unsupported key")
	}

	var cl map[string]any
	if json.Unmarshal(pb, &cl) != nil {
		return nil, errors.New("malformed JWT payload")
	}
	if iss, _ := cl["iss"].(string); strings.TrimRight(iss, "/") != c.cfg.Issuer {
		return nil, errors.New("wrong issuer")
	}
	if !audienceOK(cl["aud"], c.cfg.ClientID, cl["azp"]) {
		return nil, errors.New("not issued for this client")
	}
	now := c.now()
	exp, ok := cl["exp"].(float64)
	if !ok || now.After(time.Unix(int64(exp), 0).Add(60*time.Second)) {
		return nil, errors.New("expired")
	}
	if nbf, ok := cl["nbf"].(float64); ok && now.Before(time.Unix(int64(nbf), 0).Add(-60*time.Second)) {
		return nil, errors.New("not valid yet")
	}
	if iat, ok := cl["iat"].(float64); ok && time.Unix(int64(iat), 0).After(now.Add(5*time.Minute)) {
		return nil, errors.New("issued in the future")
	}
	if n, _ := cl["nonce"].(string); n == "" || n != wantNonce {
		return nil, errors.New("wrong nonce")
	}
	sub, _ := cl["sub"].(string)
	if sub == "" {
		return nil, errors.New("no subject")
	}
	out := &Claims{Subject: sub}
	out.Email, _ = cl["email"].(string)
	out.Email = normEmail(out.Email)
	out.EmailVerified = truthy(cl["email_verified"])
	out.Name, _ = cl["name"].(string)
	if out.Name == "" {
		out.Name, _ = cl["preferred_username"].(string)
	}
	if g, ok := cl[c.cfg.GroupsClaim].([]any); ok {
		for _, x := range g {
			if s, ok := x.(string); ok {
				out.Groups = append(out.Groups, s)
			}
		}
	}
	return out, nil
}

func audienceOK(aud any, clientID string, azp any) bool {
	switch a := aud.(type) {
	case string:
		return a == clientID
	case []any:
		found := false
		for _, x := range a {
			if s, _ := x.(string); s == clientID {
				found = true
			}
		}
		if !found {
			return false
		}
		// With several audiences the token must say it was issued to this client.
		if len(a) > 1 {
			p, _ := azp.(string)
			return p == clientID
		}
		return true
	}
	return false
}

// truthy accepts the boolean or the string "true": some providers send either.
func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.EqualFold(x, "true")
	}
	return false
}

// ---- turning verified claims into a session ---------------------------------

// LoginSSO signs a user in with verified claims, creating or linking an
// account as configured.
func (s *Service) LoginSSO(cl *Claims) (*User, string, error) {
	o := s.sso()
	if o == nil {
		return nil, "", errors.New("single sign-on is not configured")
	}
	cfg := o.cfg
	u := s.store.UserByIdentity("oidc", cl.Subject)
	if u == nil && cl.Email != "" {
		if existing := s.store.UserByEmail(cl.Email); existing != nil {
			// Linking by email is only safe if the provider verified that address.
			if !cl.EmailVerified {
				return nil, "", errors.New("your provider has not verified this email address, so it cannot be linked to the existing account")
			}
			linked, err := s.store.UpdateUser(existing.ID, func(x *User) {
				x.Identities = append(x.Identities, Identity{Provider: "oidc", Subject: cl.Subject})
			})
			if err != nil {
				return nil, "", err
			}
			u = linked
		}
	}
	admin := false
	for _, e := range cfg.AdminEmails {
		if normEmail(e) == cl.Email && cl.EmailVerified {
			admin = true
		}
	}
	for _, g := range cl.Groups {
		if cfg.AdminGroup != "" && g == cfg.AdminGroup {
			admin = true
		}
	}
	if u == nil {
		if !cfg.AutoCreate {
			return nil, "", errors.New("no BLASTA account is linked to this sign-in, and automatic account creation is off")
		}
		if cl.Email == "" || !validEmail(cl.Email) {
			return nil, "", errors.New("your provider did not share an email address, which BLASTA needs")
		}
		if !s.domainAllowed(cl.Email) {
			return nil, "", ErrDomain
		}
		name := cl.Name
		if name == "" {
			name = strings.SplitN(cl.Email, "@", 2)[0]
		}
		s.regMu.Lock()
		role, status := RoleUser, cfg.Trust
		if admin || (s.store.Count() == 0 && s.conf().SetupToken == "") {
			role, status = RoleAdmin, StatusActive
		}
		nu := &User{ID: newID(), Email: cl.Email, Name: name, Role: role, Status: status, CreatedAt: s.now(),
			Identities: []Identity{{Provider: "oidc", Subject: cl.Subject}}}
		err := s.store.CreateUser(nu)
		s.regMu.Unlock()
		if err != nil {
			return nil, "", err
		}
		u = nu
	} else if admin && u.Role != RoleAdmin {
		var err error
		if u, err = s.store.UpdateUser(u.ID, func(x *User) { x.Role = RoleAdmin }); err != nil {
			return nil, "", err
		}
	}
	switch u.Status {
	case StatusUnverified:
		return nil, "", ErrUnverified
	case StatusPending:
		return nil, "", ErrPending
	case StatusDisabled:
		return nil, "", ErrDisabled
	}
	return s.startSession(u)
}
