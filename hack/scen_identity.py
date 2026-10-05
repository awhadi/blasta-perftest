# ======================================================================
# Identity: OIDC / OAuth 2.0, SAML 2.0 and LDAP.
# Executed by genpresets.py (shares its helpers: var, job, add, S30 ...).
# ======================================================================
FORM = {"Content-Type": "application/x-www-form-urlencoded", "Accept": "application/json"}
NOFOLLOW = {"followRedirects": False}
PKCE = "code_challenge=E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM&code_challenge_method=S256"
OK_REDIR = [200, 302, 303]
SAAS = ("This is a hosted identity service: check the vendor's load-testing policy and your tenant's rate "
        "limits before running, keep the rate low, and expect 429s. ")


def ivars(extra=None, secret_env="OIDC_CLIENT_SECRET"):
    v = [var("url", "https://idp.example.com", "Issuer / base URL, no trailing slash", placeholder=True),
         var("clientId", "blasta-test", "A test client registered for load testing", placeholder=True),
         var("clientSecret", "${" + secret_env + "}", "Client secret. Read from the " + secret_env + " env var by the CLI.", sensitive=True),
         var("redirectUri", "https://app.example.com/callback", "A redirect URI registered on that client", placeholder=True),
         var("scope", "openid%20profile%20email", "Space-separated scopes, URL-encoded"),
         var("token", "${OIDC_ACCESS_TOKEN}", "A valid access token. Read from OIDC_ACCESS_TOKEN by the CLI.", sensitive=True)]
    return v + (extra or [])


def oidc(pid, title, summary, stack, paths, extra_vars=None, secret_env="OIDC_CLIENT_SECRET", saas=False,
         cc_body_extra="", rate=1, extra_jobs=None, category="Identity", refresh=True, ropc=False):
    """Build a full OIDC/OAuth preset from a map of endpoint paths. A missing
    path means the provider has no such endpoint, so the job is not generated."""
    U = "{{url}}"
    note = SAAS if saas else ""
    r = lambda n: max(1, int(n * rate))
    cs = "client_id={{clientId}}&client_secret={{clientSecret}}"
    jobs = []

    def add_job(cond, *a, **k):
        if cond:
            jobs.append(job(*a, **k))

    d = paths.get("discovery")
    add_job(d, "discovery", "OIDC discovery document",
            note + "Fetched by every relying party at start-up and on a timer. Cheap, but often served uncached.",
            "read", {"url": U + d}, concurrency=10, rps=r(50), expect=[200], headers=JSONH)
    add_job(d, "discovery-nocache", "discovery document (cache bypass)",
            note + "Forces the origin to build the document. Shows the cost behind a CDN or cache.",
            "read", {"url": U + d}, concurrency=10, rps=r(20), expect=[200], headers=dict(JSONH, **NOCACHE))
    add_job(paths.get("jwks"), "jwks", "JSON web key set",
            note + "Every API validating tokens locally refreshes keys from here, and a key rotation causes a thundering herd of fetches.",
            "read", {"url": U + (paths.get("jwks") or "")}, concurrency=10, rps=r(50), expect=[200], headers=JSONH)

    a = paths.get("authorize")
    q = "?response_type=code&client_id={{clientId}}&redirect_uri={{redirectUri}}&scope={{scope}}&state=blasta&nonce=blasta&" + PKCE
    add_job(a, "authorize-redirect", "authorization endpoint (login page or redirect)",
            note + "Start of every browser login. Redirects are NOT followed, so this measures the provider itself, not the login page it forwards to.",
            "read", {"url": U + (a or "") + q}, concurrency=10, rps=r(20), expect=OK_REDIR, meta=NOFOLLOW, headers=HTML)
    add_job(a, "authorize-silent", "silent authentication (prompt=none)",
            note + "What SPAs do in a hidden iframe to renew a session. With no session it must answer with a login_required redirect, quickly, and without rendering a page.",
            "read", {"url": U + (a or "") + q + "&prompt=none"}, concurrency=10, rps=r(20), expect=OK_REDIR, meta=NOFOLLOW, headers=HTML)
    add_job(a, "authorize-bad-client", "authorization with an unknown client",
            note + "Error path: an unregistered client_id must be refused cheaply. If it costs as much as a real login, attackers can load you for free.",
            "read", {"url": U + (a or "") + "?response_type=code&client_id=blasta-no-such-client&redirect_uri=https://example.invalid/cb&scope=openid&state=x"},
            concurrency=10, rps=r(20), expect=[200, 302, 303, 400, 401, 403, 404], meta=NOFOLLOW, headers=HTML)
    add_job(a, "authorize-bad-redirect", "authorization with a wrong redirect_uri",
            note + "Must be rejected with an error page, never redirected (open-redirect check): a 302 to the bad URI would be a vulnerability, so only 4xx and the error page count as success.",
            "read", {"url": U + (a or "") + "?response_type=code&client_id={{clientId}}&redirect_uri=https://evil.example.invalid/cb&scope=openid&state=x"},
            concurrency=5, rps=r(10), expect=[200, 400, 401, 403], meta=NOFOLLOW, headers=HTML)

    t = paths.get("token")
    add_job(t, "token-client-credentials", "token: client credentials",
            note + "Machine-to-machine tokens. Signing a JWT is CPU bound, so this is where most providers saturate first. Creates tokens and sessions: staging only.",
            "mutating", {"url": U + (t or "")}, method="POST",
            body="grant_type=client_credentials&" + cs + "&scope={{scope}}" + cc_body_extra, headers=FORM,
            concurrency=10, rps=r(20), timeout=T30, expect=[200])
    add_job(t, "token-basic-auth", "token: client credentials with HTTP Basic auth",
            note + "Same grant, client authenticated with an Authorization: Basic header. Replace the header value with base64(clientId:secret) when running from the UI.",
            "mutating", {"url": U + (t or "")}, method="POST",
            body="grant_type=client_credentials&scope={{scope}}" + cc_body_extra,
            headers=dict(FORM, **{"Authorization": "Basic ${OIDC_CLIENT_BASIC}"}), concurrency=10, rps=r(20), timeout=T30, expect=[200])
    add_job(t, "token-wrong-secret", "token: wrong client secret",
            note + "Failed client authentication. Should be fast, rate limited and logged. Keep the rate low: some providers lock the client after repeated failures.",
            "read", {"url": U + (t or "")}, method="POST",
            body="grant_type=client_credentials&client_id={{clientId}}&client_secret=blasta-wrong-secret", headers=FORM,
            concurrency=5, rps=r(10), expect=[400, 401])
    add_job(t, "token-invalid-code", "token: invalid authorization code",
            note + "A bogus code at the token endpoint (invalid_grant). Replayed or guessed codes land here, so it must stay cheap.",
            "read", {"url": U + (t or "")}, method="POST",
            body="grant_type=authorization_code&code=blasta-invalid-code&redirect_uri={{redirectUri}}&" + cs + "&code_verifier=blasta", headers=FORM,
            concurrency=10, rps=r(20), expect=[400, 401])
    add_job(t and refresh, "token-refresh", "token: refresh token grant",
            note + "Token renewal, the most frequent token call in a long-lived app. If refresh tokens ROTATE, only the first request succeeds and the rest fail with invalid_grant: use a client without rotation.",
            "mutating", {"url": U + (t or "")}, method="POST",
            body="grant_type=refresh_token&refresh_token=${OIDC_REFRESH_TOKEN}&" + cs, headers=FORM,
            concurrency=10, rps=r(20), timeout=T30, expect=[200])
    add_job(t and ropc, "token-password", "token: password grant (ROPC)",
            "Legacy direct login with a username and password. Password hashing is deliberately slow, so this is the heaviest token call. Needs ${OIDC_USERNAME} and ${OIDC_PASSWORD}. Creates sessions: staging only.",
            "mutating", {"url": U + (t or "")}, method="POST",
            body="grant_type=password&username=${OIDC_USERNAME}&password=${OIDC_PASSWORD}&scope={{scope}}&" + cs, headers=FORM,
            concurrency=5, rps=r(5), timeout=T30, expect=[200])
    add_job(t, "token-exchange", "token: exchange (RFC 8693)",
            note + "Swaps a token for another (service-to-service delegation). Not every provider supports it; a 400 unsupported_grant_type is counted as an error.",
            "mutating", {"url": U + (t or "")}, method="POST",
            body="grant_type=urn:ietf:params:oauth:grant-type:token-exchange&subject_token={{token}}&subject_token_type=urn:ietf:params:oauth:token-type:access_token&" + cs,
            headers=FORM, concurrency=5, rps=r(10), timeout=T30, expect=[200])
    add_job(t, "cors-preflight", "CORS preflight on the token endpoint",
            note + "Browser apps send an OPTIONS before calling the token endpoint. It must be answered at the edge, not by the identity code.",
            "read", {"url": U + (t or "")}, method="OPTIONS", concurrency=10, rps=r(50), expect=[200, 204],
            headers={"Origin": "{{redirectUri}}", "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "content-type"})
    add_job(t, "token-ramp", "token endpoint capacity ramp",
            note + "Ramps client-credentials requests up to find the rate where latency or errors climb: your token-issuing capacity. Staging only.",
            "mutating", {"url": U + (t or "")}, method="POST",
            body="grant_type=client_credentials&" + cs + "&scope={{scope}}" + cc_body_extra, headers=FORM,
            concurrency=100, rps=r(150), ramp=M5, duration=M5 + M2, timeout=T30, expect=[200], queueSize=5000, maxWorkers=300)

    i = paths.get("introspect")
    add_job(i, "introspect", "token introspection",
            note + "Opaque-token APIs call this on every request, so its latency is added to your whole API. Needs a valid token.",
            "read", {"url": U + (i or "")}, method="POST", body="token={{token}}&" + cs, headers=FORM,
            concurrency=20, rps=r(100), expect=[200])
    add_job(i, "introspect-invalid", "introspection of an invalid token",
            note + "Probing with garbage tokens must return active=false quickly. Expensive here means a cheap amplification attack.",
            "read", {"url": U + (i or "")}, method="POST", body="token=blasta-invalid-token&" + cs, headers=FORM,
            concurrency=20, rps=r(100), expect=[200])
    add_job(paths.get("revoke"), "revoke", "token revocation",
            note + "Logout and security flows. Revoking an unknown token must still return 200 (RFC 7009).",
            "write", {"url": U + (paths.get("revoke") or "")}, method="POST",
            body="token=blasta-invalid-token&token_type_hint=access_token&" + cs, headers=FORM,
            concurrency=10, rps=r(20), expect=[200])

    u = paths.get("userinfo")
    add_job(u, "userinfo", "userinfo with a valid token",
            note + "Called by apps after login and by gateways per request. Needs a valid token.",
            "read", {"url": U + (u or "")}, concurrency=20, rps=r(100), expect=[200],
            headers=dict(JSONH, **{"Authorization": "Bearer {{token}}"}))
    add_job(u, "userinfo-invalid", "userinfo with an invalid token",
            note + "Rejection path: should be a fast 401.",
            "read", {"url": U + (u or "")}, concurrency=20, rps=r(100), expect=[401, 403],
            headers=dict(JSONH, **{"Authorization": "Bearer blasta-invalid-token"}))
    add_job(u, "userinfo-no-token", "userinfo without a token",
            note + "Unauthenticated requests: also a fast 401, and a good WAF/rate-limit canary.",
            "read", {"url": U + (u or "")}, concurrency=20, rps=r(100), expect=[401, 403], headers=JSONH)
    add_job(u, "userinfo-soak", "userinfo soak (10 min)",
            note + "Steady load for ten minutes: finds cache expiry, connection and memory problems in token validation.",
            "read", {"url": U + (u or "")}, concurrency=20, rps=r(50), duration=M10, expect=[200], queueSize=5000,
            headers=dict(JSONH, **{"Authorization": "Bearer {{token}}"}))

    add_job(paths.get("device"), "device-authorization", "device authorization (device flow)",
            note + "Starts a TV or CLI login. Creates a pending device code per request, so it also tests the cleanup of abandoned ones. Staging only.",
            "mutating", {"url": U + (paths.get("device") or "")}, method="POST",
            body="scope={{scope}}&" + cs, headers=FORM, concurrency=5, rps=r(10), expect=[200])
    add_job(paths.get("par"), "pushed-authorization-request", "pushed authorization request (PAR)",
            note + "FAPI-style clients push the request first. Stores the request server-side, so watch memory and expiry. Staging only.",
            "mutating", {"url": U + (paths.get("par") or "")}, method="POST",
            body="response_type=code&redirect_uri={{redirectUri}}&scope={{scope}}&state=blasta&" + PKCE + "&" + cs,
            headers=FORM, concurrency=5, rps=r(10), expect=[200, 201])
    add_job(paths.get("endsession"), "end-session", "logout (end session)",
            note + "RP-initiated logout without a session: shows a confirmation page or redirects. Redirects are not followed.",
            "read", {"url": U + (paths.get("endsession") or "")}, concurrency=10, rps=r(20), expect=OK_REDIR, meta=NOFOLLOW, headers=HTML)
    add_job(paths.get("register"), "dynamic-registration", "dynamic client registration",
            "Creates a client per request. Only enable this endpoint where you need it: it is an unauthenticated write. Staging only; clean the clients up afterwards.",
            "write", {"url": U + (paths.get("register") or "")}, method="POST",
            body="{\"client_name\":\"blasta load test\",\"redirect_uris\":[\"https://example.invalid/cb\"],\"grant_types\":[\"authorization_code\"]}",
            headers={"Content-Type": "application/json", "Accept": "application/json"}, concurrency=2, rps=r(2), expect=[200, 201])
    add_job(paths.get("health"), "health", "provider health / readiness",
            "What your load balancer polls. It should stay fast even while tokens are being issued.",
            "read", {"url": U + (paths.get("health") or "")}, concurrency=5, rps=r(20), expect=[200])
    for ej in (extra_jobs or []):
        jobs.append(ej)

    vs = ivars(extra_vars, secret_env)
    add(pid, title, category, summary, stack, vs, jobs)


# ---------------------------------------------------------------- generic OIDC / OAuth 2.0
oidc("oidc-generic", "OIDC / OAuth 2.0 (any provider)",
     "Every standard OIDC and OAuth endpoint: discovery, keys, authorize, tokens, introspection, userinfo, device, PAR, logout.",
     "Any OpenID Connect provider",
     {"discovery": "/.well-known/openid-configuration", "jwks": "/jwks.json", "authorize": "/authorize", "token": "/token",
      "userinfo": "/userinfo", "introspect": "/introspect", "revoke": "/revoke", "endsession": "/logout",
      "device": "/device/code", "par": "/par", "register": "/register", "health": "/health"}, ropc=True,
     extra_jobs=[job("oauth-metadata", "OAuth server metadata (RFC 8414)",
                     "The non-OIDC discovery document. Many providers answer 404, which is fine.",
                     "read", {"url": "{{url}}/.well-known/oauth-authorization-server"}, concurrency=10, rps=20, expect=[200, 404], headers=JSONH)])

# ---------------------------------------------------------------- Keycloak (OIDC part)
oidc("keycloak", "Keycloak (OIDC)",
     "Keycloak realm endpoints, tokens, admin API and health, with every OIDC scenario.",
     "Keycloak 22+ (include /auth in the URL on legacy installs)",
     {"discovery": "/realms/{{realm}}/.well-known/openid-configuration",
      "jwks": "/realms/{{realm}}/protocol/openid-connect/certs",
      "authorize": "/realms/{{realm}}/protocol/openid-connect/auth",
      "token": "/realms/{{realm}}/protocol/openid-connect/token",
      "userinfo": "/realms/{{realm}}/protocol/openid-connect/userinfo",
      "introspect": "/realms/{{realm}}/protocol/openid-connect/token/introspect",
      "revoke": "/realms/{{realm}}/protocol/openid-connect/revoke",
      "endsession": "/realms/{{realm}}/protocol/openid-connect/logout",
      "device": "/realms/{{realm}}/protocol/openid-connect/auth/device",
      "par": "/realms/{{realm}}/protocol/openid-connect/ext/par/request",
      "register": "/realms/{{realm}}/clients-registrations/openid-connect"}, ropc=True,
     extra_vars=[var("realm", "master", "Realm name"),
                 var("mgmt", "https://keycloak.example.com:9000", "Management interface URL (port 9000 on Keycloak 25+)", placeholder=True),
                 var("adminToken", "${KC_ADMIN_TOKEN}", "An admin access token. Read from KC_ADMIN_TOKEN by the CLI.", sensitive=True)],
     extra_jobs=[
         job("realm-info", "realm public info", "GET /realms/{realm}: public key and endpoints, polled by older adapters.",
             "read", {"url": "{{url}}/realms/{{realm}}"}, concurrency=10, rps=30, expect=[200], headers=JSONH),
         job("uma-discovery", "UMA 2 discovery", "Authorization-services metadata document.",
             "read", {"url": "{{url}}/realms/{{realm}}/.well-known/uma2-configuration"}, concurrency=10, rps=30, expect=[200], headers=JSONH),
         job("account-console", "account console", "The self-service SPA shell users open to manage their profile.",
             "read", {"url": "{{url}}/realms/{{realm}}/account/"}, concurrency=10, rps=20, expect=[200, 302, 303], meta=NOFOLLOW, headers=HTML),
         job("admin-users", "admin REST: list users", "Back-office calls are far heavier than login calls: a user list with a search joins several tables. Needs an admin token.",
             "read", {"url": "{{url}}/admin/realms/{{realm}}/users?max=20"}, concurrency=5, rps=10, timeout=T30, expect=[200],
             headers=dict(JSONH, **{"Authorization": "Bearer {{adminToken}}"})),
         job("admin-users-count", "admin REST: count users", "Counting is a full table scan on large realms.",
             "read", {"url": "{{url}}/admin/realms/{{realm}}/users/count"}, concurrency=5, rps=5, timeout=T30, expect=[200],
             headers=dict(JSONH, **{"Authorization": "Bearer {{adminToken}}"})),
         job("health-live", "health: live (management port)", "Kubernetes liveness probe target.",
             "read", {"url": "{{mgmt}}/health/live"}, concurrency=5, rps=20, expect=[200], headers=JSONH),
         job("health-ready", "health: ready (management port)", "Readiness includes the database connection, so it fails first when the DB is overloaded.",
             "read", {"url": "{{mgmt}}/health/ready"}, concurrency=5, rps=20, expect=[200], headers=JSONH),
         job("metrics", "metrics scrape (management port)", "Prometheus scrape: a large body that grows with realm count.",
             "read", {"url": "{{mgmt}}/metrics"}, concurrency=2, rps=2, expect=[200]),
     ])

# ---------------------------------------------------------------- Hosted / self-hosted OIDC providers
oidc("okta", "Okta", "Org authorization server: discovery, keys, authorize, token, userinfo, introspect, revoke.",
     "Okta (hosted)", {"discovery": "/oauth2/{{authServer}}/.well-known/openid-configuration",
                       "jwks": "/oauth2/{{authServer}}/v1/keys", "authorize": "/oauth2/{{authServer}}/v1/authorize",
                       "token": "/oauth2/{{authServer}}/v1/token", "userinfo": "/oauth2/{{authServer}}/v1/userinfo",
                       "introspect": "/oauth2/{{authServer}}/v1/introspect", "revoke": "/oauth2/{{authServer}}/v1/revoke",
                       "endsession": "/oauth2/{{authServer}}/v1/logout", "device": "/oauth2/{{authServer}}/v1/device/authorize"},
     extra_vars=[var("authServer", "default", "Authorization server id (use 'default' for the built-in one)")], saas=True, rate=0.2)
oidc("auth0", "Auth0", "Tenant endpoints including the audience-based client credentials flow.",
     "Auth0 (hosted)", {"discovery": "/.well-known/openid-configuration", "jwks": "/.well-known/jwks.json",
                        "authorize": "/authorize", "token": "/oauth/token", "userinfo": "/userinfo",
                        "revoke": "/oauth/revoke", "endsession": "/v2/logout", "device": "/oauth/device/code", "par": "/oauth/par"},
     saas=True, rate=0.2, cc_body_extra="&audience=https://api.example.com")
oidc("entra-id", "Microsoft Entra ID (Azure AD)", "v2.0 endpoints for one tenant: discovery, keys, authorize, token, device code.",
     "Microsoft identity platform", {"discovery": "/{{tenant}}/v2.0/.well-known/openid-configuration",
                                     "jwks": "/{{tenant}}/discovery/v2.0/keys", "authorize": "/{{tenant}}/oauth2/v2.0/authorize",
                                     "token": "/{{tenant}}/oauth2/v2.0/token", "device": "/{{tenant}}/oauth2/v2.0/devicecode",
                                     "endsession": "/{{tenant}}/oauth2/v2.0/logout"},
     extra_vars=[var("tenant", "contoso.onmicrosoft.com", "Tenant id or domain", placeholder=True)], saas=True, rate=0.1)
oidc("cognito", "AWS Cognito", "User pool discovery and hosted UI endpoints.",
     "Amazon Cognito", {"discovery": "/{{poolId}}/.well-known/openid-configuration", "jwks": "/{{poolId}}/.well-known/jwks.json"},
     extra_vars=[var("poolId", "us-east-1_example", "User pool id, such as us-east-1_AbCdEf", placeholder=True),
                 var("hostedUi", "https://example.auth.us-east-1.amazoncognito.com", "Hosted UI domain, no trailing slash", placeholder=True)],
     saas=True, rate=0.1, refresh=False,
     extra_jobs=[
         job("hosted-login", "hosted UI login page", "The Cognito-hosted login form.",
             "read", {"url": "{{hostedUi}}/login?response_type=code&client_id={{clientId}}&redirect_uri={{redirectUri}}"}, concurrency=5, rps=5, expect=OK_REDIR, meta=NOFOLLOW, headers=HTML),
         job("hosted-token", "hosted UI token: client credentials", "Counts against Cognito's token request quota.",
             "mutating", {"url": "{{hostedUi}}/oauth2/token"}, method="POST",
             body="grant_type=client_credentials&client_id={{clientId}}&client_secret={{clientSecret}}&scope={{scope}}", headers=FORM, concurrency=5, rps=5, expect=[200]),
         job("hosted-userinfo", "hosted UI userinfo", "Validates a user access token.",
             "read", {"url": "{{hostedUi}}/oauth2/userInfo"}, concurrency=5, rps=10, expect=[200], headers=dict(JSONH, **{"Authorization": "Bearer {{token}}"})),
     ])
oidc("authentik", "authentik", "Per-application OIDC provider, flows and health.",
     "authentik", {"discovery": "/application/o/{{slug}}/.well-known/openid-configuration", "jwks": "/application/o/{{slug}}/jwks/",
                   "authorize": "/application/o/authorize/", "token": "/application/o/token/", "userinfo": "/application/o/userinfo/",
                   "introspect": "/application/o/introspect/", "revoke": "/application/o/revoke/",
                   "endsession": "/application/o/{{slug}}/end-session/", "device": "/application/o/device/", "health": "/-/health/ready/"},
     extra_vars=[var("slug", "my-app", "Application slug", placeholder=True)],
     extra_jobs=[job("flow-login", "default authentication flow", "The login UI shell; each request starts a flow plan in the cache.",
                     "read", {"url": "{{url}}/if/flow/default-authentication-flow/"}, concurrency=10, rps=15, expect=[200], headers=HTML),
                 job("health-live", "health: live", "Liveness endpoint.", "read", {"url": "{{url}}/-/health/live/"}, concurrency=5, rps=20, expect=[200])])
oidc("authelia", "Authelia", "OIDC provider plus the forward-auth endpoint your reverse proxy calls on every request.",
     "Authelia", {"discovery": "/.well-known/openid-configuration", "jwks": "/jwks.json", "authorize": "/api/oidc/authorization",
                  "token": "/api/oidc/token", "userinfo": "/api/oidc/userinfo", "introspect": "/api/oidc/introspection",
                  "revoke": "/api/oidc/revocation", "par": "/api/oidc/pushed-authorization-request", "device": "/api/oidc/device-authorization",
                  "health": "/api/health"}, refresh=True,
     extra_jobs=[
         job("forward-auth", "forward-auth check (unauthenticated)",
             "Nginx, Traefik or Caddy call this for EVERY proxied request, so it is your highest-volume endpoint. Unauthenticated requests are answered with 401 or a redirect.",
             "read", {"url": "{{url}}/api/authz/forward-auth"}, concurrency=50, rps=200, expect=[200, 302, 303, 401, 403], meta=NOFOLLOW,
             headers={"X-Forwarded-Proto": "https", "X-Forwarded-Host": "app.example.com", "X-Forwarded-Uri": "/", "X-Forwarded-Method": "GET"}),
         job("state", "session state", "Frontend poll of the session state.", "read", {"url": "{{url}}/api/state"}, concurrency=10, rps=30, expect=[200, 401], headers=JSONH)])
oidc("dex", "Dex", "Dex OIDC provider with its connectors.", "Dex",
     {"discovery": "/.well-known/openid-configuration", "jwks": "/keys", "authorize": "/auth", "token": "/token",
      "userinfo": "/userinfo", "introspect": "/token/introspect", "device": "/device/code", "health": "/healthz"})
oidc("zitadel", "ZITADEL", "ZITADEL OIDC endpoints and health.", "ZITADEL",
     {"discovery": "/.well-known/openid-configuration", "jwks": "/oauth/v2/keys", "authorize": "/oauth/v2/authorize",
      "token": "/oauth/v2/token", "userinfo": "/oidc/v1/userinfo", "introspect": "/oauth/v2/introspect",
      "revoke": "/oauth/v2/revoke", "endsession": "/oidc/v1/end_session", "device": "/oauth/v2/device_authorization", "health": "/debug/ready"})
oidc("ory-hydra", "Ory Hydra", "Hydra public API (and admin introspection) endpoints.", "Ory Hydra",
     {"discovery": "/.well-known/openid-configuration", "jwks": "/.well-known/jwks.json", "authorize": "/oauth2/auth",
      "token": "/oauth2/token", "userinfo": "/userinfo", "revoke": "/oauth2/revoke", "endsession": "/oauth2/sessions/logout",
      "device": "/oauth2/device/auth", "register": "/oauth2/register", "health": "/health/ready"},
     extra_jobs=[job("admin-introspect", "admin: introspect (port 4445)", "Resource servers call the ADMIN api to introspect. Point url at the admin port for this job only, and never expose it publicly.",
                     "read", {"url": "{{url}}/admin/oauth2/introspect"}, method="POST", body="token={{token}}", headers=FORM, concurrency=20, rps=100, expect=[200])])
oidc("fusionauth", "FusionAuth", "FusionAuth OAuth endpoints, status and JWT validation API.", "FusionAuth",
     {"discovery": "/.well-known/openid-configuration", "jwks": "/.well-known/jwks.json", "authorize": "/oauth2/authorize",
      "token": "/oauth2/token", "userinfo": "/oauth2/userinfo", "introspect": "/oauth2/introspect", "revoke": "/oauth2/revoke",
      "endsession": "/oauth2/logout", "device": "/oauth2/device_authorize", "health": "/api/status"},
     extra_jobs=[job("jwt-validate", "API: validate JWT", "Backends call this to validate a token server-side. Needs a valid access token.",
                     "read", {"url": "{{url}}/api/jwt/validate"}, concurrency=20, rps=100, expect=[200], headers={"Authorization": "Bearer {{token}}"})])

# ---------------------------------------------------------------- SAML
SAML_VARS = lambda sso, slo, meta: [
    var("metadataUrl", meta, "URL of the IdP's SAML metadata", placeholder=True),
    var("ssoUrl", sso, "The IdP single sign-on URL (HTTP-Redirect / POST binding)", placeholder=True),
    var("sloUrl", slo, "The IdP single logout URL", placeholder=True),
    var("spEntityId", "https://sp.example.com/saml/metadata", "Entity ID of a service provider registered at the IdP", placeholder=True),
    var("acsUrl", "https://sp.example.com/saml/acs", "That service provider's assertion consumer URL", placeholder=True),
    var("nameId", "blasta-test-user", "NameID used in logout requests"),
    var("samlRequest", "", "AuthnRequest, HTTP-Redirect binding (built when rendered)", derived="saml-authnrequest-redirect"),
    var("samlRequestPassive", "", "AuthnRequest with IsPassive", derived="saml-authnrequest-redirect-passive"),
    var("samlRequestForce", "", "AuthnRequest with ForceAuthn", derived="saml-authnrequest-redirect-force"),
    var("samlRequestPost", "", "AuthnRequest, HTTP-POST binding", derived="saml-authnrequest-post"),
    var("samlRequestUnknownSp", "", "AuthnRequest from an unregistered SP", derived="saml-authnrequest-redirect-unknownsp"),
    var("samlLogout", "", "LogoutRequest, HTTP-Redirect binding", derived="saml-logoutrequest-redirect"),
]
SAMLNOTE = ("The AuthnRequest is generated when you render this template, unsigned, with a fresh ID and the current time: "
            "render it shortly before the run, and register the SP entity ID at the IdP first. ")


def saml_idp(pid, title, summary, stack, sso, slo, meta, extra_jobs=None, extra_vars=None):
    S = "{{ssoUrl}}"
    FORMH = {"Content-Type": "application/x-www-form-urlencoded", "Accept": "text/html"}
    J = [
        job("metadata", "IdP metadata", "Fetched by every SP at start-up and refreshed on a timer; with many SPs it is polled constantly.",
            "read", {"url": "{{metadataUrl}}"}, concurrency=10, rps=30, expect=[200], headers={"Accept": "application/samlmetadata+xml, application/xml"}),
        job("metadata-nocache", "IdP metadata (cache bypass)", "Forces the origin to generate and sign the metadata.",
            "read", {"url": "{{metadataUrl}}"}, concurrency=5, rps=10, expect=[200], headers=dict(NOCACHE, Accept="application/xml")),
        job("sso-redirect", "SP-initiated SSO (HTTP-Redirect binding)",
            SAMLNOTE + "The start of every SAML login: the IdP parses the request, looks up the SP and shows a login page or continues an existing session. Redirects are not followed.",
            "read", {"url": S + "?SAMLRequest={{samlRequest}}&RelayState=blasta"}, concurrency=10, rps=20, expect=OK_REDIR, meta=NOFOLLOW, headers=HTML),
        job("sso-post", "SP-initiated SSO (HTTP-POST binding)",
            SAMLNOTE + "Same login, sent as a form POST. Some IdPs handle the two bindings in different code paths.",
            "read", {"url": S}, method="POST", body="SAMLRequest={{samlRequestPost}}&RelayState=blasta", headers=FORMH,
            concurrency=10, rps=20, expect=OK_REDIR, meta=NOFOLLOW),
        job("sso-passive", "passive SSO (IsPassive)",
            SAMLNOTE + "Must answer WITHOUT showing a login page: either a response or a NoPassive status. Used by silent session checks.",
            "read", {"url": S + "?SAMLRequest={{samlRequestPassive}}&RelayState=blasta"}, concurrency=10, rps=20, expect=OK_REDIR, meta=NOFOLLOW, headers=HTML),
        job("sso-force-authn", "forced re-authentication (ForceAuthn)",
            SAMLNOTE + "Ignores the existing session and demands a fresh login: the login page is rendered every time.",
            "read", {"url": S + "?SAMLRequest={{samlRequestForce}}&RelayState=blasta"}, concurrency=10, rps=15, expect=OK_REDIR, meta=NOFOLLOW, headers=HTML),
        job("sso-unknown-sp", "request from an unknown SP",
            SAMLNOTE + "Error path: an unregistered entity ID must be refused cheaply with an error page, never redirected to its ACS URL.",
            "read", {"url": S + "?SAMLRequest={{samlRequestUnknownSp}}&RelayState=blasta"}, concurrency=10, rps=20,
            expect=[200, 400, 403, 404], meta=NOFOLLOW, headers=HTML),
        job("sso-malformed", "malformed SAMLRequest",
            "Garbage that is not deflated XML. The IdP must answer with a 4xx or an error page and must not log stack traces per request or crash.",
            "read", {"url": S + "?SAMLRequest=bm90LXNhbWw%3D&RelayState=blasta"}, concurrency=10, rps=20,
            expect=[200, 400, 403], meta=NOFOLLOW, headers=HTML),
        job("sso-missing-request", "SSO endpoint with no request",
            "Bots and health checks hit the SSO URL bare. It should return an error page quickly.",
            "read", {"url": S}, concurrency=10, rps=30, expect=[200, 400, 403, 404, 405], meta=NOFOLLOW, headers=HTML),
        job("slo-redirect", "single logout request (HTTP-Redirect)",
            SAMLNOTE + "Logout of a session that does not exist: should return a logout response or an error page quickly. The SP also needs a single-logout URL registered at the IdP: in testing, a Keycloak client without one answered 500 ('uri parameter is null') instead of a clean error, which this job will show as errors.",
            "read", {"url": "{{sloUrl}}?SAMLRequest={{samlLogout}}&RelayState=blasta"}, concurrency=10, rps=15,
            expect=[200, 302, 303, 400], meta=NOFOLLOW, headers=HTML),
        job("sso-spike", "login spike (10x in 10 s)",
            SAMLNOTE + "Everyone logs in at 09:00. Reaches a high rate in ten seconds and holds, then watch recovery.",
            "read", {"url": S + "?SAMLRequest={{samlRequest}}&RelayState=blasta"}, concurrency=200, rps=300, ramp=S10, duration=M2,
            expect=OK_REDIR, meta=NOFOLLOW, queueSize=5000, maxWorkers=500, headers=HTML),
        job("sso-ramp", "login capacity ramp",
            SAMLNOTE + "Slow ramp to find the rate where the IdP's latency climbs. Staging only.",
            "read", {"url": S + "?SAMLRequest={{samlRequest}}&RelayState=blasta"}, concurrency=100, rps=100, ramp=M5, duration=M5 + M2,
            expect=OK_REDIR, meta=NOFOLLOW, queueSize=5000, maxWorkers=300, headers=HTML),
    ]
    J += (extra_jobs or [])
    add(pid, title, "SAML", summary, stack, SAML_VARS(sso, slo, meta) + (extra_vars or []), J)


saml_idp("saml-idp", "SAML 2.0 IdP (any provider)",
         "Metadata, SP-initiated SSO in both bindings, passive and forced login, unknown and malformed requests, logout, spike and ramp.",
         "Any SAML 2.0 identity provider",
         "https://idp.example.com/saml/sso", "https://idp.example.com/saml/slo", "https://idp.example.com/saml/metadata",
         extra_vars=[var("idpInitiatedUrl", "https://idp.example.com/saml/idp-initiated?sp=sp", "IdP-initiated SSO URL for a registered SP", placeholder=True)],
         extra_jobs=[job("idp-initiated", "IdP-initiated SSO", "Login that starts at the IdP portal. Needs an existing session to produce a response, so without one it returns the login page.",
                         "read", {"url": "{{idpInitiatedUrl}}"}, concurrency=10, rps=20, expect=[200, 302, 303, 400, 403], meta=NOFOLLOW, headers=HTML)])
saml_idp("keycloak-saml", "Keycloak (SAML)", "Keycloak realm acting as a SAML IdP: descriptor, SSO, IdP-initiated login and logout.",
         "Keycloak SAML", "https://keycloak.example.com/realms/master/protocol/saml", "https://keycloak.example.com/realms/master/protocol/saml",
         "https://keycloak.example.com/realms/master/protocol/saml/descriptor",
         extra_vars=[var("idpInitiatedUrl", "https://keycloak.example.com/realms/master/protocol/saml/clients/my-client", "IdP-initiated URL: .../protocol/saml/clients/<client URL name>", placeholder=True)],
         extra_jobs=[job("idp-initiated", "IdP-initiated SSO", "Keycloak's IdP-initiated endpoint for a client.",
                         "read", {"url": "{{idpInitiatedUrl}}"}, concurrency=10, rps=20, expect=[200, 302, 303, 400, 403], meta=NOFOLLOW, headers=HTML)])
saml_idp("shibboleth-idp", "Shibboleth IdP", "Shibboleth Identity Provider v4/v5: metadata, SSO bindings, unsolicited SSO, logout and status.",
         "Shibboleth IdP", "https://idp.example.com/idp/profile/SAML2/Redirect/SSO", "https://idp.example.com/idp/profile/SAML2/Redirect/SLO",
         "https://idp.example.com/idp/shibboleth",
         extra_vars=[var("idpInitiatedUrl", "https://idp.example.com/idp/profile/SAML2/Unsolicited/SSO?providerId=https://sp.example.com/saml/metadata", "Unsolicited SSO URL", placeholder=True),
                     var("statusUrl", "https://idp.example.com/idp/status", "Status page (usually limited to admin IPs)", placeholder=True)],
         extra_jobs=[job("idp-initiated", "unsolicited SSO", "IdP-initiated login. Without a session this renders the login flow.",
                         "read", {"url": "{{idpInitiatedUrl}}"}, concurrency=10, rps=20, expect=[200, 302, 303, 400, 403], meta=NOFOLLOW, headers=HTML),
                     job("status", "status page", "Prints metrics and configuration, so it is expensive and restricted: 403 from outside the allowed IPs is expected.",
                         "read", {"url": "{{statusUrl}}"}, concurrency=2, rps=2, expect=[200, 403])])
saml_idp("simplesamlphp-idp", "SimpleSAMLphp IdP", "SimpleSAMLphp as an IdP. Every request creates a PHP session file, so watch disk and inodes.",
         "PHP + SimpleSAMLphp", "https://idp.example.com/simplesaml/saml2/idp/SSOService.php",
         "https://idp.example.com/simplesaml/saml2/idp/SingleLogoutService.php", "https://idp.example.com/simplesaml/saml2/idp/metadata.php",
         extra_jobs=[job("static-resource", "theme resource", "Static files served through PHP in some setups: the cheapest request on the site.",
                         "read", {"url": "https://idp.example.com/simplesaml/resources/default.css"}, concurrency=20, rps=100, expect=[200, 404])])
saml_idp("adfs", "AD FS (SAML, WS-Fed and OIDC)", "AD FS federation metadata, SAML SSO, WS-Federation sign-in, OIDC and the load balancer probe.",
         "Windows Server AD FS", "https://adfs.example.com/adfs/ls/", "https://adfs.example.com/adfs/ls/",
         "https://adfs.example.com/FederationMetadata/2007-06/FederationMetadata.xml",
         extra_vars=[var("adfsUrl", "https://adfs.example.com", "AD FS base URL, no trailing slash", placeholder=True)],
         extra_jobs=[
             job("probe", "load balancer probe", "/adfs/probe is what a load balancer polls; it must stay a fast 200 under load.",
                 "read", {"url": "{{adfsUrl}}/adfs/probe"}, concurrency=5, rps=20, expect=[200]),
             job("wsfed-signin", "WS-Federation sign-in", "wsignin1.0 request for a relying party, the older but still common protocol.",
                 "read", {"url": "{{adfsUrl}}/adfs/ls/?wa=wsignin1.0&wtrealm=urn:blasta:test&wctx=blasta"}, concurrency=10, rps=15, expect=OK_REDIR, meta=NOFOLLOW, headers=HTML),
             job("idp-initiated", "IdP-initiated sign-on page", "The portal page listing relying parties.",
                 "read", {"url": "{{adfsUrl}}/adfs/ls/idpinitiatedsignon"}, concurrency=10, rps=15, expect=[200, 302], meta=NOFOLLOW, headers=HTML),
             job("oidc-discovery", "OIDC discovery", "AD FS 2016+ OpenID Connect metadata.",
                 "read", {"url": "{{adfsUrl}}/adfs/.well-known/openid-configuration"}, concurrency=10, rps=30, expect=[200], headers=JSONH),
             job("oidc-keys", "OIDC signing keys", "Keys that APIs use to validate tokens.",
                 "read", {"url": "{{adfsUrl}}/adfs/discovery/keys"}, concurrency=10, rps=30, expect=[200], headers=JSONH),
         ])

# ---- SAML service providers
add("saml-sp", "SAML service provider (Shibboleth SP / mod_auth_mellon / SimpleSAMLphp SP)", "SAML",
    "What the SP does per request: session check, redirect to the IdP, ACS handling, metadata and logout.",
    "Shibboleth SP, mod_auth_mellon, SimpleSAMLphp SP, Spring Security SAML",
    [var("url", "https://sp.example.com", "SP base URL, no trailing slash", placeholder=True),
     var("protectedPath", "/secure/", "A path protected by the SP"),
     var("publicPath", "/", "A public path on the same site"),
     var("metadataPath", "/Shibboleth.sso/Metadata", "SP metadata path"),
     var("acsPath", "/Shibboleth.sso/SAML2/POST", "Assertion consumer service path"),
     var("sessionPath", "/Shibboleth.sso/Session", "SP session status path"),
     var("loginPath", "/Shibboleth.sso/Login", "SP login handler path"),
     var("logoutPath", "/Shibboleth.sso/Logout", "SP logout handler path")],
    [
        job("metadata", "SP metadata", "Fetched by IdPs, federations and monitoring.", "read",
            {"url": "{{url}}{{metadataPath}}"}, concurrency=10, rps=30, expect=[200], headers={"Accept": "application/xml"}),
        job("public-page", "public page behind the SP", "Baseline: the SP module is in the request path but does not require a session.",
            "read", {"url": "{{url}}{{publicPath}}"}, concurrency=20, rps=100, headers=HTML),
        job("protected-redirect", "protected page without a session",
            "The SP builds an AuthnRequest and redirects to the IdP. This is every new visitor's first request. Redirects are not followed.",
            "read", {"url": "{{url}}{{protectedPath}}"}, concurrency=20, rps=50, expect=[302, 303], meta=NOFOLLOW, headers=HTML),
        job("login-handler", "SP login handler", "Starts SP-initiated login explicitly.",
            "read", {"url": "{{url}}{{loginPath}}?target={{protectedPath}}"}, concurrency=10, rps=30, expect=[200, 302, 303, 400], meta=NOFOLLOW, headers=HTML),
        job("session-status", "session status", "Polled by SPAs to see whether the user is logged in.",
            "read", {"url": "{{url}}{{sessionPath}}"}, concurrency=10, rps=50, expect=[200, 401, 403, 404], headers=HTML),
        job("acs-garbage", "ACS with a garbage SAMLResponse",
            "Anyone can POST to the ACS URL. The SP must reject an invalid response cheaply (no signature check should run) and without crashing. A 4xx or the SP's error page counts as rejected.",
            "read", {"url": "{{url}}{{acsPath}}"}, method="POST", body="SAMLResponse=bm90LXNhbWw%3D&RelayState=blasta",
            headers={"Content-Type": "application/x-www-form-urlencoded", "Accept": "text/html"}, concurrency=10, rps=30,
            expect=[200, 302, 400, 403, 500], meta=NOFOLLOW),
        job("acs-empty", "ACS with an empty POST", "Bots do this constantly.", "read", {"url": "{{url}}{{acsPath}}"}, method="POST",
            body="", headers={"Content-Type": "application/x-www-form-urlencoded"}, concurrency=10, rps=30, expect=[200, 302, 400, 403, 405, 500], meta=NOFOLLOW),
        job("logout-handler", "SP logout handler", "Logout of a session that does not exist.",
            "read", {"url": "{{url}}{{logoutPath}}"}, concurrency=10, rps=20, expect=[200, 302, 303], meta=NOFOLLOW, headers=HTML),
        job("sp-ramp", "unauthenticated traffic ramp",
            "Ramps requests to a protected page to find where the SP and its session store slow down. Each request creates a session or a redirect record.",
            "read", {"url": "{{url}}{{protectedPath}}"}, concurrency=100, rps=300, ramp=M5, duration=M5 + M2, expect=[302, 303], meta=NOFOLLOW,
            queueSize=5000, maxWorkers=300, headers=HTML),
    ])

# ---------------------------------------------------------------- LDAP
def tlv(tag, content):
    n = len(content)
    if n < 128:
        ln = bytes([n])
    else:
        b = n.to_bytes((n.bit_length() + 7) // 8, "big")
        ln = bytes([0x80 | len(b)]) + b
    return bytes([tag]) + ln + content


def ber_int(v, tag=0x02):
    return tlv(tag, bytes([v]))


_bind = tlv(0x30, ber_int(1) + tlv(0x60, ber_int(3) + tlv(0x04, b"") + tlv(0x80, b"")))      # anonymous simple bind v3
_bind_ok = tlv(0x30, ber_int(1) + tlv(0x61, tlv(0x0a, b"\x00") + tlv(0x04, b"") + tlv(0x04, b"")))
_search = tlv(0x30, ber_int(2) + tlv(0x63, tlv(0x04, b"") + tlv(0x0a, b"\x00") + tlv(0x0a, b"\x00") + ber_int(0) + ber_int(0)
                                    + tlv(0x01, b"\x00") + tlv(0x87, b"objectClass") + tlv(0x30, b"")))            # RootDSE, (objectClass=*)
_unbind = tlv(0x30, ber_int(3) + tlv(0x42, b""))
_ok_prefix = _bind_ok.hex()
_ok_bind_then_entry = _bind_ok.hex() + "30"        # bind OK, then the first byte of a searchResEntry
LD = {"url": "tcp://{{host}}:{{port}}"}
add("ldap", "LDAP / Active Directory", "Directory",
    "Connection capacity, anonymous bind and RootDSE search with the replies checked, plus the connect-per-login pattern.",
    "OpenLDAP, 389 Directory Server, Active Directory, FreeIPA",
    [var("host", "ldap.example.com", "LDAP server host name", placeholder=True),
     var("port", "389", "LDAP port (389). LDAPS on 636 needs TLS, which only the connect test covers."),
     var("ldapsPort", "636", "LDAPS port")],
    [
        job("connect", "connection accept", "Measures only how fast the directory accepts a TCP connection: a connection-limit and listen-backlog test.",
            "read", LD, executor="tcp", concurrency=50, rps=200),
        job("ldaps-connect", "LDAPS connection accept", "TCP accept on the TLS port. The TLS handshake itself is not performed.",
            "read", {"url": "tcp://{{host}}:{{ldapsPort}}"}, executor="tcp", concurrency=20, rps=50),
        job("anonymous-bind", "anonymous bind", "Sends a real LDAP v3 anonymous bind and checks for resultCode success. Many servers refuse anonymous binds on purpose: unexpected_reply errors then mean the hardening works, not that the server is down.",
            "read", LD, executor="tcp", meta={"bodyHex": _bind.hex(), "expectHex": _ok_prefix}, concurrency=20, rps=100),
        job("rootdse-search", "bind then RootDSE search", "Bind plus a base-scope search of the RootDSE, the cheapest realistic query (what applications run to discover server capabilities). Checks the bind succeeded and a search entry follows. Every request is a fresh connection, which is also how many apps and Keycloak user federation behave per login.",
            "read", LD, executor="tcp", meta={"bodyHex": _bind.hex() + _search.hex(), "expectHex": _ok_bind_then_entry}, concurrency=20, rps=100),
        job("ramp", "connection ramp", "Ramps bind and search to find where the directory starts refusing or slowing down.",
            "read", LD, executor="tcp", meta={"bodyHex": _bind.hex() + _search.hex(), "expectHex": _ok_bind_then_entry},
            concurrency=100, rps=500, ramp=M5, duration=M5 + M2, queueSize=5000, maxWorkers=300),
        job("soak", "bind and search soak (10 min)", "Steady load that surfaces connection leaks and replication lag.",
            "read", LD, executor="tcp", meta={"bodyHex": _bind.hex() + _search.hex(), "expectHex": _ok_bind_then_entry},
            concurrency=20, rps=50, duration=M10, queueSize=5000),
    ])
