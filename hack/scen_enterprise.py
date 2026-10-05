# ======================================================================
# Enterprise test plan, generated for EVERY template.
#
# For each preset this picks one representative request (the "reference job")
# and derives the standard enterprise sequence from it:
#
#   01 smoke   02 baseline   03 average load   04 peak load   05 stress
#   06 spike   07 recovery   08 soak           09 breakpoint  10 failover window
#
# plus protocol extras and a curated list of real-world scenario jobs. Jobs carry
# an `slo` block, so `blasta run job.json` exits 2 when a gate is missed.
# ======================================================================
import copy

SEC = 1_000_000_000
MS = 1_000_000

# Which job in each preset represents "normal traffic". Anything not listed falls
# back to the first read-only job that is not an error-path or admin job.
REF = {
    "wordpress": "single-post", "joomla": "article", "drupal": "node", "ghost": "post", "mediawiki": "main",
    "typo3": "page", "magento": "product", "prestashop": "product", "shopware": "product", "woocommerce": "product",
    "discourse": "topic-json", "phpbb": "viewtopic", "nextjs": "route", "static-site": "page", "lamp": "app",
    "strapi": "rest-list", "api-rest": "list", "api-graphql": "query", "grpc-generic": "unary",
    "websocket-generic": "handshake", "tcp-generic": "connect", "postgres-read": "indexed", "mysql-read": "indexed",
    "postgres-write": "insert", "mysql-write": "insert", "wordpress-db": "recent-posts",
    "test-patterns": "average-load", "cache-cdn": "hit", "health-and-limits": "under-limit", "auth-api": "bearer",
    "oidc-generic": "authorize-redirect", "keycloak": "authorize-redirect", "okta": "authorize-redirect",
    "auth0": "authorize-redirect", "entra-id": "authorize-redirect", "cognito": "authorize-redirect",
    "authentik": "authorize-redirect", "authelia": "authorize-redirect", "dex": "authorize-redirect",
    "zitadel": "authorize-redirect", "ory-hydra": "authorize-redirect", "fusionauth": "authorize-redirect",
    "saml-idp": "sso-redirect", "keycloak-saml": "sso-redirect", "shibboleth-idp": "sso-redirect",
    "simplesamlphp-idp": "sso-redirect", "adfs": "sso-redirect", "saml-sp": "protected-redirect",
    "ldap": "rootdse-search", "redis": "set-get", "redis-sentinel": "get-master", "memcached": "get-hit",
    "rabbitmq": "amqp-handshake", "mqtt": "connect-311", "nats": "info", "mail-servers": "smtp-banner",
    "mail-web": "login-page", "rest-patterns": "list", "soap-services": "soap11", "etcd": "range",
    "consul": "health-service", "vault": "token-lookup", "minio": "get-object", "clickhouse": "select-one",
    "influxdb": "select-recent", "couchdb": "all-docs", "elasticsearch": "match", "meilisearch": "search",
    "realtime": "socketio-polling", "observability": "prom-instant", "tcp-services": "redis-ping",
    "nextcloud": "status", "moodle": "front", "laravel": "home", "django": "home", "rails": "home", "java-web": "home",
}
SAAS_IDS = {"okta", "auth0", "entra-id", "cognito"}
AVOID = ("admin", "search", "login", "ramp", "soak", "capacity", "spike", "stress", "breakpoint", "flood", "wrong",
         "invalid", "malformed", "garbage", "empty", "missing", "not-found", "unauth", "notfound", "bad", "unknown",
         "no-token", "options", "cors", "head")
# Latency targets by executor. They are defaults: replace them with your own SLO.
LAT = {"http": (1000, 2000), "sql": (250, 1000), "tcp": (50, 200), "grpc": (300, 1000), "ws": (500, 1500)}


def _ref(p):
    want = REF.get(p["id"])
    for j in p["jobs"]:
        if j["id"] == want:
            return j
    for j in p["jobs"]:
        if j["safety"] == "read" and j["job"].get("rps", 0) > 0 and not any(a in j["id"] for a in AVOID):
            return j
    return None


def _slo(executor, err, p95=True, mult=1.0):
    p95ms, p99ms = LAT.get(executor, LAT["http"])
    s = {"maxErrorRate": err}
    if p95:
        s["maxP95"] = int(p95ms * mult * MS)
        s["maxP99"] = int(p99ms * mult * MS)
    return s


def _derive(p, ref, jid, label, notes, rps, conc, dur, ramp=0, slo=None, headers=None, method=None, expect=None,
            body=None, meta=None, executor_extra=None):
    j = copy.deepcopy(ref["job"])
    base_rps = ref["job"].get("rps", 0) or 0
    j["name"] = "%s: %s" % (p["title"], label)
    j["rps"] = max(0, int(rps))
    j["concurrency"] = max(1, min(1000, int(conc)))
    j["duration"] = int(dur)
    if ramp:
        j["ramp"] = int(ramp)
    else:
        j.pop("ramp", None)
    j["queueSize"] = max(j.get("queueSize", 1000), min(50000, j["rps"] * 5), 1000)
    j["maxWorkers"] = min(2000, max(j.get("maxWorkers", 200), j["concurrency"] * 2))
    j["timeout"] = max(j.get("timeout", 15 * SEC), 5 * SEC)
    if headers:
        j["headers"] = dict(j.get("headers") or {}, **headers)
    if method:
        j["method"] = method
    if expect is not None:
        j["expectStatus"] = expect
    if body is not None:
        j["body"] = body
    if meta:
        j.setdefault("target", {}).setdefault("meta", {}).update(meta)
    if slo:
        j["slo"] = slo
    else:
        j.pop("slo", None)
    note = notes + " Reference request: %s." % ref["name"]
    if ref["safety"] in ("write", "mutating"):
        note += " This job WRITES or creates data on every request: staging only, and expect a lot of rows."
    if p["id"] in SAAS_IDS:
        note += " Hosted identity service: this stays well below the vendor's rate limits; check their load-testing policy first."
    return {"id": jid, "name": j["name"], "notes": note, "safety": ref["safety"], "job": j}


def _plan(p, ref):
    ex = ref["job"].get("executor", "http")
    R = ref["job"].get("rps", 0) or 50
    R = max(R, 10)
    C = max(ref["job"].get("concurrency", 10), 2)
    saas = p["id"] in SAAS_IDS
    out = []
    A = lambda *a, **k: out.append(_derive(p, ref, *a, **k))

    A("ent-01-smoke", "01 smoke", "Enterprise plan, step 1 of 10. One request a second for 30 seconds. Run this first, every time: it proves the address, credentials and headers are right and that the environment is up before any real load is applied. Gate: zero errors.",
      1, 1, 30 * SEC, slo=_slo(ex, 0, p95=False))
    A("ent-02-baseline", "02 baseline (20% load)", "Step 2 of 10. About a fifth of normal traffic for 5 minutes: the uncontended latency of this request. Every later result is judged against it, so record p50 and p95. Gate: at most 0.5% errors and the default latency targets.",
      max(2, R // 5), max(1, C // 5), 300 * SEC, slo=_slo(ex, 0.5))
    A("ent-03-average-load", "03 average load (SLO check)", "Step 3 of 10. Normal busy-hour traffic for 10 minutes. The rate is the reference job's rate: raise it to your measured production peak-hour rate. This is the run that proves (or breaks) your SLO. Gate: at most 1% errors, p95 and p99 inside the targets.",
      R, C, 600 * SEC, slo=_slo(ex, 1))
    A("ent-04-peak-load", "04 peak load (2x average)", "Step 4 of 10. Twice the average for 10 minutes: the busiest hour of the year plus headroom. Latency may rise, but must stay in SLO; if it does not, you have no headroom. Gate: at most 2% errors, latency targets doubled.",
      R * 2, C * 2, 600 * SEC, slo=_slo(ex, 2, mult=2))
    if saas:
        A("ent-08-soak", "08 soak (30 min at 60%)", "Step 8 of 10, shortened for a hosted service. Steady load for 30 minutes to catch token or session expiry problems and slow decay on your side.",
          max(1, int(R * 0.6)), max(1, int(C * 0.6)), 1800 * SEC, slo=_slo(ex, 1))
        return out
    A("ent-05-stress", "05 stress (ramp to 4x)", "Step 5 of 10. Ramps to four times average over 10 minutes, then holds for 2. Finds where it degrades and HOW: gracefully (latency rises, errors stay low) or badly (errors, timeouts, crashes, restarts). Observation only, no gate.",
      R * 4, C * 4, 720 * SEC, ramp=600 * SEC)
    A("ent-06-spike", "06 spike (10x in 10 s)", "Step 6 of 10. Reaches ten times average within 10 seconds and holds for 2 minutes: a campaign email, a news link, a failover. Checks autoscaling, queue limits and load shedding. Gate: at most 5% errors, because shedding load is acceptable and crashing is not.",
      R * 10, C * 10, 130 * SEC, ramp=10 * SEC, slo=_slo(ex, 5, p95=False))
    A("ent-07-recovery", "07 recovery after the spike", "Step 7 of 10. Run IMMEDIATELY after the spike, at average load for 5 minutes. Latency and errors must return to the baseline from step 2. If they do not, something is stuck: queues, connection pools, GC, an autoscaler cool-down. Gate: same as average load.",
      R, C, 300 * SEC, slo=_slo(ex, 1))
    A("ent-08-soak", "08 soak (1 hour at 60%)", "Step 8 of 10. One hour of steady load. Finds leaks and slow decay in memory, connections, file descriptors, disk, log volume and cache churn. Watch the resource graphs: any line that climbs and never flattens is a finding. Gate: at most 0.5% errors.",
      max(1, int(R * 0.6)), max(1, int(C * 0.6)), 3600 * SEC, slo=_slo(ex, 0.5))
    A("ent-09-breakpoint", "09 breakpoint (find the ceiling)", "Step 9 of 10. Ramps to twenty times average over 20 minutes. Stop it when errors pass about 5%: the rate at that moment is your ceiling, and ceiling divided by peak is your capacity margin. Use a production-like environment, never production.",
      R * 20, C * 20, 1380 * SEC, ramp=1200 * SEC)
    A("ent-10-failover-window", "10 resilience window (failover / deploy)", "Step 10 of 10. Average load for 15 minutes. About 5 minutes in, cause the event you are testing: kill a pod or node, fail over the database, roll out a new version, drain a zone. Errors in the window are your real availability loss. Gate: at most 1% errors overall; read the time series for how long the dip lasted.",
      R, C, 900 * SEC, slo=_slo(ex, 1))

    # ---- protocol-specific extras
    method = ref["job"].get("method", "GET")
    if ex == "http" and method == "GET":
        A("ent-x-connection-close", "no keep-alive (new connection per request)", "Sends Connection: close, so every request pays for a new TCP and TLS handshake: the cost for clients that do not reuse connections (scripts, some mobile SDKs, health checkers). Shows load balancer and TLS termination limits.",
          R, C, 300 * SEC, headers={"Connection": "close"}, slo=_slo(ex, 1, mult=1.5))
        A("ent-x-bot-traffic", "crawler traffic (Googlebot user agent)", "Same request identified as a search crawler. Tests WAF and bot-management rules and whether crawlers get cached or origin responses. Crawlers can easily be a third of all traffic.",
          R, C, 300 * SEC, headers={"User-Agent": "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"}, slo=_slo(ex, 1))
        A("ent-x-large-headers", "large cookies (~4 KB header)", "Adds a 4 KB Cookie header, like a logged-in user with many tracking cookies. Proxies and servers reject headers around 8 KB, so this shows how close you are to that limit.",
          max(2, R // 2), C, 300 * SEC, headers={"Cookie": "blasta=" + "x" * 4000}, slo=_slo(ex, 1))
        A("ent-x-monitor-storm", "uptime monitors (HEAD from many locations)", "A fleet of synthetic monitors checking the URL with HEAD requests every 30 seconds from 20 locations, plus load balancer probes. Cheap each, constant in total.",
          max(20, R), C, 600 * SEC, method="HEAD", expect=[200, 204, 301, 302, 304, 405], slo=_slo(ex, 1))
    if ex == "sql":
        pool = (ref["job"].get("db") or {}).get("maxOpen", 8)
        A("ent-x-pool-exhaustion", "connection pool exhaustion", "Runs four times more workers than the pool has connections, so most requests queue for a connection. Shows how the database and your pool behave when the app is slower than the traffic: latency should rise smoothly, not fall off a cliff.",
          R * 2, max(pool * 4, 16), 300 * SEC, slo=_slo(ex, 2, mult=4))
    if ex in ("tcp", "ws", "grpc"):
        A("ent-x-connection-storm", "connection storm (ramp to 10x)", "Ramps new connections per second to ten times normal: a fleet reconnecting after an outage, a deploy that restarts every pod, a network blip. Shows accept-queue, file descriptor and handshake limits.",
          R * 10, min(C * 10, 500), 420 * SEC, ramp=120 * SEC)
    return out


# ---------------------------------------------------------------- curated real-world scenarios
# (preset ids, job id, new id, label, notes, rps_mult, conc_mult, duration s, ramp s, slo(err, p95 factor or None))
OIDC = ["oidc-generic", "keycloak", "okta", "auth0", "entra-id", "cognito", "authentik", "authelia", "dex", "zitadel", "ory-hydra", "fusionauth"]
SAML_IDP = ["saml-idp", "keycloak-saml", "shibboleth-idp", "simplesamlphp-idp", "adfs"]
SHOP = ["magento", "prestashop", "shopware", "woocommerce"]
SC = [
    (OIDC, "authorize-redirect", "ent-x-login-storm", "Monday 9am login storm", "Everyone in the company signs in within a minute of starting work. Ramps login starts to eight times normal over 60 seconds. The identity provider is the one system every other system depends on, so this is the most important identity test.", 8, 8, 300, 60, (5, None)),
    (OIDC, "token-client-credentials", "ent-x-token-expiry-herd", "token expiry herd", "Many services were started together, so their tokens expire together and they all ask for new ones in the same second. Jumps token requests to ten times normal in 10 seconds.", 10, 10, 120, 10, (5, None)),
    (OIDC, "introspect", "ent-x-gateway-introspection-peak", "API gateway introspection peak", "An API gateway that introspects opaque tokens adds one introspection call to every business request, so the identity provider carries the whole API's traffic. Three times normal for 10 minutes.", 3, 3, 600, 0, (0.5, 1.0)),
    (OIDC, "jwks", "ent-x-key-rotation-herd", "signing-key rotation herd", "After a signing key rotates, every service that validates tokens refetches the key set at once. Twenty times normal for 90 seconds; this should be a cache hit at the edge.", 20, 10, 90, 5, (1, 1.0)),
    (OIDC, "userinfo", "ent-x-userinfo-peak", "userinfo peak", "Apps call userinfo after login and on every page load. Three times normal for 10 minutes.", 3, 3, 600, 0, (0.5, 1.0)),
    (OIDC, "token-wrong-secret", "ent-x-credential-stuffing-resilience", "credential-stuffing resilience", "Ten times the normal rate of failed client authentication for 2 minutes, the shape of a credential-stuffing attack. The provider must stay responsive for real users, rate-limit or lock the attacker, and not spend expensive hashing on each attempt. Run a normal login in parallel to confirm it keeps working. Staging only: it can lock the client.", 10, 5, 120, 0, None),
    (SAML_IDP, "sso-redirect", "ent-x-login-storm", "Monday 9am login storm", "Every employee opens their SAML-protected apps at the start of the day. Ramps SP-initiated logins to eight times normal over 60 seconds.", 8, 8, 300, 60, (5, None)),
    (SAML_IDP, "sso-post", "ent-x-post-binding-peak", "POST-binding login peak", "Same login via the POST binding, which some SPs use exclusively. Three times normal for 10 minutes.", 3, 3, 600, 0, (1, 1.0)),
    (SAML_IDP, "metadata", "ent-x-metadata-herd", "metadata refresh herd", "After an IdP certificate rollover every SP refreshes metadata at once. Ten times normal for 90 seconds.", 10, 10, 90, 5, (1, 1.0)),
    (SAML_IDP, "slo-redirect", "ent-x-logout-wave", "end-of-day logout wave", "Single logout requests arrive in a wave when sessions time out together. Three times normal for 10 minutes.", 3, 3, 600, 0, (2, None)),
    (["saml-sp"], "protected-redirect", "ent-x-session-expiry-wave", "session expiry wave", "All sessions were created in the morning, so they expire together and every user is redirected to the IdP at the same moment. Ramps unauthenticated hits to ten times normal.", 10, 10, 180, 10, (5, None)),
    (["saml-sp"], "acs-garbage", "ent-x-acs-abuse", "ACS endpoint abuse", "The ACS URL accepts POSTs from anyone. Ten times normal garbage submissions for 2 minutes: the SP must reject them cheaply and keep serving real users.", 10, 5, 120, 0, None),
    (["ldap"], "rootdse-search", "ent-x-auth-peak", "authentication peak", "Applications authenticate users against the directory on every login. Three times normal for 10 minutes. Watch connection counts and replication lag.", 3, 3, 600, 0, (1, 1.0)),
    (["ldap"], "anonymous-bind", "ent-x-bind-storm", "bind storm", "A fleet of applications reconnects after a restart. Ramps binds to ten times normal over 2 minutes.", 10, 10, 300, 120, None),
    (["redis"], "get", "ent-x-cache-aside-peak", "cache-aside read peak", "Five times normal reads for 10 minutes: the application's busiest hour with a warm cache. p95 should stay in single-digit milliseconds.", 5, 5, 600, 0, (0.1, 0.1)),
    (["redis"], "incr", "ent-x-hot-key-contention", "hot key contention", "Every request increments the SAME counter at high concurrency, like a global rate limiter or a page-view counter. Redis is single-threaded, so one hot key caps throughput however many cores you have.", 10, 10, 120, 0, None),
    (["redis"], "set-get", "ent-x-write-through-peak", "write-through peak", "Three times normal write-then-read for 10 minutes: session stores and write-through caches.", 3, 3, 600, 0, (0.1, 0.2)),
    (["redis"], "connect-ping-quit", "ent-x-connection-storm", "client connection storm", "Applications without a connection pool reconnect for every operation. Ramps to ten times normal. Watch maxclients, file descriptors and TIME_WAIT sockets.", 10, 10, 300, 120, None),
    (["memcached"], "get-hit", "ent-x-cache-peak", "cache read peak", "Five times normal reads for 10 minutes with a warm cache.", 5, 5, 600, 0, (0.1, 0.1)),
    (["memcached"], "get-miss", "ent-x-miss-storm", "miss storm (database shield)", "Five times normal reads that all MISS, as after a cache flush or restart. Each miss is a database query in real life, so this is the load your database sees on a cold cache.", 5, 5, 300, 30, None),
    (["memcached"], "multiget", "ent-x-fanout-peak", "multi-get fan-out peak", "Page renders that fetch many keys at once. Three times normal for 10 minutes.", 3, 3, 600, 0, (0.2, 0.5)),
    (["rest-patterns", "api-rest"], "list", "ent-x-list-peak", "list endpoint peak", "Three times normal for 10 minutes on the most-used endpoint.", 3, 3, 600, 0, (1, 1.0)),
    (["rest-patterns"], "pagination-deep", "ent-x-deep-pagination-stress", "deep pagination stress", "Ramps requests for page 200 to four times normal. Offset pagination gets slower the deeper you go: a scraper walking the whole collection can take the API down.", 4, 4, 360, 240, None),
    (["rest-patterns"], "create", "ent-x-write-peak", "write peak", "Twice normal creates for 10 minutes: an import job or a busy ordering hour. Staging only.", 2, 2, 600, 0, (1, 2.0)),
    (["rest-patterns"], "item", "ent-x-hot-item", "hot item (everyone reads the same record)", "Ten times normal concurrency on ONE id: a viral item, a homepage banner, a config record. Exposes row-lock contention, cache stampedes and missing HTTP caching.", 10, 10, 120, 0, None),
    (["rest-patterns"], "filter", "ent-x-search-peak", "filtered search peak", "Twice normal filtered lists for 10 minutes; unindexed filters are what fall over first.", 2, 2, 600, 0, (1, 2.0)),
    (["soap-services"], "soap11", "ent-x-peak", "peak (2x)", "Twice normal for 10 minutes. XML parsing is CPU bound, so SOAP services often saturate a core long before the network.", 2, 2, 600, 0, (1, 2.0)),
    (["soap-services"], "wsdl", "ent-x-wsdl-herd", "WSDL refetch herd", "Clients refetch the WSDL when they restart, so a rolling restart of 50 client instances is a burst of WSDL requests. Ten times normal for 90 seconds.", 10, 10, 90, 5, (2, 2.0)),
    (["soap-services"], "ws-security", "ent-x-auth-peak", "authenticated peak", "Three times normal authenticated calls for 10 minutes. Token validation is often the slow part.", 3, 3, 600, 0, (1, 2.0)),
    (SHOP, "product", "ent-x-flash-sale", "flash sale (10x in 10 s)", "A sale starts at a published time and everyone opens the same product page. Ramps to ten times normal in 10 seconds and holds for 3 minutes. Watch for the page cache being bypassed by cookies or query strings.", 10, 10, 190, 10, (5, None)),
    (["woocommerce"], "cart-fragments", "ent-x-cart-peak", "cart fragments peak", "The cart fragments AJAX call runs on every page for visitors with a cart and bypasses page caching. Three times normal for 10 minutes.", 3, 3, 600, 0, (1, 2.0)),
    (["woocommerce"], "checkout", "ent-x-checkout-stress", "checkout page stress", "Ramps the checkout page to three times normal over 5 minutes. The page loads payment gateways and shipping rates, so it is far heavier than a product page.", 3, 3, 420, 300, None),
    (["wordpress"], "single-post", "ent-x-viral-post", "viral post (10x in 10 s)", "A post is shared widely and traffic arrives within seconds. Uncached, so PHP and the database take the full hit. Ramps to ten times normal.", 10, 10, 190, 10, (5, None)),
    (["mail-servers"], "smtp-banner", "ent-x-inbound-peak", "inbound mail peak", "Three times normal connection attempts for 10 minutes: a newsletter bounce wave or a spam run. Watch per-IP connection limits and the deferred queue.", 3, 3, 600, 0, None),
    (["mail-servers"], "imap-banner", "ent-x-morning-login-wave", "morning mailbox login wave", "Every mail client reconnects at the start of the working day. Ramps to five times normal over 2 minutes.", 5, 5, 300, 120, None),
    (["mail-web"], "login-page", "ent-x-morning-login-wave", "morning webmail wave", "Everyone opens webmail at the same time. Ramps to eight times normal in 60 seconds.", 8, 8, 300, 60, (5, None)),
    (["postgres-write", "mysql-write"], "insert", "ent-x-ingest-peak", "ingest peak", "Three times normal inserts for 10 minutes: a batch import or a busy ordering hour. Writes to the target table. Staging only.", 3, 3, 600, 0, (1, 2.0)),
    (["etcd"], "range", "ent-x-api-server-read-peak", "control-plane read peak", "Five times normal reads for 10 minutes: Kubernetes API servers and controllers hammer etcd.", 5, 5, 600, 0, (0.5, 1.0)),
    (["etcd"], "put", "ent-x-write-peak", "write peak", "Twice normal writes for 10 minutes. Latency is bounded by the disk's fsync time.", 2, 2, 600, 0, (1, 2.0)),
    (["consul"], "health-service", "ent-x-discovery-peak", "service discovery peak", "Five times normal lookups for 10 minutes: every sidecar and consul-template instance polling at once.", 5, 5, 600, 0, (0.5, 1.0)),
    (["vault"], "token-lookup", "ent-x-secret-read-peak", "token validation peak", "Three times normal token lookups for 10 minutes. Every application start-up and renewal does this.", 3, 3, 600, 0, (0.5, 1.0)),
    (["minio"], "get-object", "ent-x-download-peak", "download peak", "Three times normal object reads for 10 minutes. Watch disk throughput and network saturation.", 3, 3, 600, 0, (1, 2.0)),
    (["rabbitmq"], "amqp-handshake", "ent-x-connection-storm", "client reconnect storm", "Every producer and consumer reconnects after a broker restart. Ramps to ten times normal.", 10, 10, 300, 120, None),
    (["mqtt"], "connect-311", "ent-x-fleet-reconnect-storm", "device fleet reconnect storm", "Thousands of devices reconnect after an outage, all at once. Ramps to twenty times normal over 3 minutes.", 20, 10, 360, 180, None),
]


def _variant(p, spec):
    ids, base_id, new_id, label, notes, rm, cm, dur, ramp, slo = spec
    if p["id"] not in ids or p["id"] in SAAS_IDS and rm > 3:
        return None
    ref = next((j for j in p["jobs"] if j["id"] == base_id), None)
    if not ref:
        return None
    R = max(ref["job"].get("rps", 0) or 50, 10)
    C = max(ref["job"].get("concurrency", 10), 2)
    ex = ref["job"].get("executor", "http")
    s = None
    if slo:
        err, f = slo
        s = _slo(ex, err, p95=f is not None, mult=f or 1.0)
    return _derive(p, ref, new_id, label, notes, R * rm, C * cm, dur * SEC, ramp=ramp * SEC, slo=s)


def _redis_pipeline(p):
    base = next((j for j in p["jobs"] if j["id"] == "set"), None)
    if not base:
        return None
    body = "".join("SET blasta:p%d v\r\n" % i for i in range(100))
    v = _derive(p, base, "ent-x-pipeline-burst", "pipeline burst (100 commands per request)",
                "Client libraries pipeline: 100 SETs in one network write, as bulk loaders and batch workers do. Checks the reply only starts correctly; throughput is 100 commands per request, so the command rate is 100 times the request rate.",
                max(5, (base["job"].get("rps", 50) or 50) // 4), 10, 300 * SEC, body=body, slo=_slo("tcp", 0.1, mult=4))
    v["job"]["target"]["meta"] = dict(v["job"]["target"].get("meta") or {}, expectPrefix="+OK")
    return v


def build_enterprise():
    added = 0
    for p in PRESETS:
        ref = _ref(p)
        if not ref:
            print("  no reference job for", p["id"])
            continue
        existing = {j["id"] for j in p["jobs"]}
        new = _plan(p, ref)
        for spec in SC:
            v = _variant(p, spec)
            if v:
                new.append(v)
        if p["id"] == "redis":
            v = _redis_pipeline(p)
            if v:
                new.append(v)
        new = [j for j in new if j["id"] not in existing]
        p["jobs"].extend(new)
        p["summary"] = (p.get("summary") or "").rstrip() + " Includes an enterprise test plan (jobs starting ent-)."
        added += len(new)
    print("enterprise jobs added: %d across %d presets" % (added, len(PRESETS)))


build_enterprise()
