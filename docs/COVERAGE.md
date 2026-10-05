# What BLASTA covers, and what it does not

BLASTA sends **independent requests at a controlled rate** and reports latency,
errors and throughput. That model covers a lot, and it has clear limits. This
page is the honest list of both, so you know when a result can be trusted.

## Covered today

77 templates, 1,803 jobs: about 720 scenario jobs plus an enterprise test plan on every template (see [ENTERPRISE.md](ENTERPRISE.md)). Run `blasta presets` for the list, or open **Start from a template** in the UI.

| Area | What you can test | Templates |
|---|---|---|
| Protocols | HTTP/HTTPS (HTTP/2), gRPC unary, WebSocket handshake and echo, raw TCP and binary protocols (with reply checks), SQL (PostgreSQL, MySQL/MariaDB) | all |
| Test shapes | Smoke, baseline, average, peak, stress, spike, recovery, soak, breakpoint and failover window with SLO gates, for every template | every template (`ent-*`), `test-patterns` |
| OIDC / OAuth 2.0 | Discovery, JWKS, authorize (redirect, silent `prompt=none`, bad client, bad redirect URI), every token grant (client credentials, Basic auth, wrong secret, invalid code, refresh, password, token exchange), introspection, revocation, userinfo (valid, invalid, missing), device flow, PAR, dynamic registration, logout, CORS preflight, token capacity ramp, soak | `oidc-generic`, `keycloak`, `okta`, `auth0`, `entra-id`, `cognito`, `authentik`, `authelia` (incl. forward-auth), `dex`, `zitadel`, `ory-hydra`, `fusionauth` |
| SAML 2.0 | IdP metadata, SP-initiated SSO in Redirect and POST bindings, passive and forced login, unknown SP, malformed request, missing request, IdP-initiated SSO, single logout, login spike and ramp. SP side: metadata, protected-page redirect, login handler, session status, ACS with garbage, logout | `saml-idp`, `keycloak-saml`, `shibboleth-idp`, `simplesamlphp-idp`, `adfs` (also WS-Fed, OIDC, probe), `saml-sp` |
| Directory | LDAP / Active Directory connection accept, anonymous bind, RootDSE search, ramp, soak | `ldap` |
| Caches | Redis / Valkey / KeyDB / Dragonfly (strings, counters, hashes, lists, sets, sorted sets, transactions, Lua, pub/sub, streams, big values, replication wait, connection churn, INFO scrape), Sentinel, Memcached (get/set, multi-get, counters, touch, CAS, large items, stats, meta protocol) | `redis`, `redis-sentinel`, `memcached` |
| Messaging | RabbitMQ (AMQP handshake, management API), MQTT 3.1.1 and 5, NATS | `rabbitmq`, `mqtt`, `nats` |
| Mail | SMTP, submission, IMAP, POP3 greetings and implicit-TLS ports, connection ramps, webmail, autodiscover and autoconfig, MTA-STS, JMAP, rspamd, Mailpit | `mail-servers`, `mail-web` |
| REST | Lists, pagination (deep offset), filter, sort, sparse fields, expand, item, 404, HEAD, OPTIONS, ETag, create, idempotency key, PUT, PATCH, DELETE, bulk, large body, multipart upload, wrong content type, malformed JSON, content negotiation, auth, OpenAPI, ramp, soak | `rest-patterns`, `api-rest`, `api-graphql`, `auth-api` |
| SOAP | WSDL, SOAP 1.1 and 1.2, WS-Security, malformed XML, unknown operation, wrong SOAPAction, empty body, wrong content type, DTD/XXE hardening, large message, ramp, soak | `soap-services` |
| Infrastructure and data services | etcd, Consul, Vault, MinIO/S3, ClickHouse, InfluxDB, CouchDB, Elasticsearch/OpenSearch, Meilisearch, Grafana/Prometheus | per-service |
| Web and reliability | Cache hit vs bypass, 304, compression, static and large files, redirects, health checks, rate limits, CORS | `cache-cdn`, `health-and-limits` |
| CMS, shops, frameworks | WordPress, WooCommerce, Joomla, Drupal, Ghost, MediaWiki, TYPO3, Magento, PrestaShop, Shopware, Strapi, Discourse, phpBB, Next.js, Laravel, Django, Rails, Spring Boot, Nextcloud, Moodle | per-stack |
| Real time | Socket.IO, SignalR | `realtime` |
| Databases | PostgreSQL and MySQL reads, writes, pool saturation, WordPress queries | `*-read`, `*-write`, `wordpress-db` |

Verified against real servers (Redis, Memcached, Mosquitto, NATS, RabbitMQ, OpenLDAP,
Keycloak): the protocol bytes, reply checks and the generated SAML requests were
accepted. The hosted-provider templates (Okta, Auth0, Entra ID, Cognito) and the
other self-hosted ones were checked for structure, not against live services.

## Not covered yet

Grouped by how much work each needs. "Engine" means a change to BLASTA itself,
not just a new template.

### Engine work, high value

1. **Multi-step user journeys.** Login, then browse, then add to cart, then
   check out. BLASTA sends each request on its own, so anything that needs state
   between requests cannot be modelled.
2. **Cookies and sessions.** There is no cookie jar. Every request is a brand
   new visitor, which is wrong for logged-in flows and (by accident) a session
   flood for apps that create a session per visit.
3. **Dynamic data.** Every request is identical, so caches answer most of them,
   and a Redis or Memcached test hits one key. Real traffic varies IDs, keys,
   search terms and query strings. It also means tests that need unique values
   (filling a cache to force eviction, creating distinct users, replay-safe
   SAML requests) cannot be written yet. Needs a data feeder (CSV or generated
   values) and per-request substitution.
4. **Extracting values from responses.** CSRF tokens, JWTs, created IDs.
5. **Response body checks.** Only the status code is checked today, so a page
   that returns 200 with an error message counts as success.
6. ~~**Pass/fail thresholds.**~~ Done: a job can carry an `slo` block (error rate,
   p95, p99), `blasta run` exits 2 on a miss, and the UI marks the result *SLO met*
   or *SLO missed*. Still missing: throughput targets, per-status rules and
   per-endpoint SLOs in a mixed run.
7. **Staged load profiles.** Only one linear ramp then a hold. No ramp up, hold,
   ramp down, or repeating waves.
8. **Mixed workloads.** Running several jobs at once in realistic proportions
   (80 % browse, 15 % search, 5 % checkout).

### Engine work, medium value

9. **Run comparison.** Compare this run with a baseline. (Saving history across
   restarts is done: set `--data-dir` or `BLASTA_DATA_DIR`; the Docker image and
   the Kubernetes manifests already do.)
10. **Think time** between steps, to model people rather than machines.
11. **Multipart file upload** and request bodies from files.
12. **Streaming:** Server-Sent Events, gRPC streaming, long-lived WebSocket
    connections that hold N open sockets rather than opening and closing them.
13. **Import** from HAR, OpenAPI, Postman or `curl` commands to build jobs fast.
14. **Scheduled runs** and notifications (a nightly smoke test).
15. **More databases:** SQLite, SQL Server, Oracle, and non-SQL stores
    (MongoDB, Redis commands rather than raw TCP).
16. **Custom TLS options per job:** client certificates, TLS version, ignoring
    certificate errors, handshake-only timing.

### Protocol limits worth knowing

- **One request per connection for TCP.** Every TCP job opens a new connection,
  sends one write and reads one reply. Multi-step dialogues (SMTP `EHLO` then
  `MAIL FROM`, IMAP login, Redis with a connection pool, MQTT publish) need the
  scripted-flow work above. Mail jobs therefore check the server greeting only.
- **No TLS for raw TCP.** The implicit-TLS ports (SMTPS 465, IMAPS 993, LDAPS
  636, `rediss://`) are tested for connection accept only.
- **SAML requests are unsigned** and use one ID per rendered file. IdPs that
  require signed requests or reject repeated IDs will refuse them; re-render
  before each run.
- **Authenticated LDAP binds** need a secret inside a binary request, which only
  the CLI can supply. Other credentials (cookies, tokens, API keys) can be pasted
  into the set-up dialog in the web UI.
- **Hosted identity providers** (Okta, Auth0, Entra ID, Cognito) are shared
  services with their own rate limits and policies. Keep rates low and read the
  vendor's load-testing rules first.

### Out of scope or needs a different tool

- **Real browser metrics** (Core Web Vitals, JavaScript execution, rendering).
  Use Lighthouse or Playwright.
- **Distributed generation** from several machines, for very high rates or
  multi-region tests. One BLASTA process is limited by one machine's network.
- **UDP, DNS, QUIC/HTTP/3, Kafka** protocols, and anything past the handshake for MQTT and AMQP (publish and consume throughput).
- **Server-side metrics** (CPU, memory, DB load on the target). BLASTA sees only
  what the client sees, so watch your monitoring alongside it.
- **Network conditions** such as added latency or packet loss.

## Reading results honestly

- Identical requests hit caches. A fast result on a cached page says little
  about your application. Use the `bypass` and `uncached` jobs to see the origin.
- One machine generating load can become the bottleneck. If BLASTA's own
  "queue wait" or "skipped" numbers rise, the generator is saturated, not the
  target.
- Test only systems you own or have permission to test. Presets marked
  `mutating` or `write` change data, so use staging.
