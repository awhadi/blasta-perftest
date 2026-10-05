# Changelog

All notable changes to blasta are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/): a fix bumps the last number, a new
feature the middle one, a breaking change the first. The current version is the
`Version` constant in `internal/version/version.go`; it is shown bottom right in the
page for signed-in people, by `blasta version`, and in `/api/health`.

## [2.3.1] - 2026-10-05

### Fixed
- `sitemap.xml` showed as raw, differently drawn XML in each browser. It now links a small
  stylesheet (`sitemap.xsl`) so people see a readable list of pages; search engines still
  read the plain XML.

## [2.3.0] - 2026-10-05

### Added
- Search engine and AI discoverability: full metadata on the home page (title, description,
  keywords, canonical address, Open Graph and Twitter cards, `WebSite` and
  `SoftwareApplication` structured data, a share image) and a no-JavaScript summary for
  crawlers that do not run scripts.
- A crawlable page for the template catalogue at `/templates/` and one for every template at
  `/templates/<id>`, listing all of its jobs with their notes, the settings it needs and
  structured data (`TechArticle`, `ItemList`, `BreadcrumbList`).
- `/robots.txt` (the API stays disallowed), `/sitemap.xml` and `/llms.txt` (a Markdown map
  of the site for AI assistants). Addresses follow `BLASTA_PUBLIC_URL` or the address the
  request came in on. All of it is generated from the built-in catalogue.

### Changed
- Visitors without an account can now browse the templates and read every job. Using a job
  still needs an account: the button says "Sign in to use". The catalogue API
  (`/api/presets`) is read-only and open; everything else is unchanged.

## [2.2.0] - 2026-10-05

### Added
- More useful `docker compose logs`: a start-up line with the effective configuration
  (version, address, sign-in, database, public URL, proxies), and events for sign-ins and
  refusals (with the reason), new accounts, password changes and resets, email
  confirmation, single sign-on, settings saved or refused, and mail server checks and test
  emails (with the error from the server when they fail).
- `BLASTA_LOG_LEVEL` (`debug`, `info`, `warn`, `error`; `BLASTA_DEBUG=true` is a shortcut
  for debug) and `BLASTA_LOG_FORMAT=json`. At `debug` every request is logged (method, path,
  status, time, client address); at `info` only failed requests are.
- Logs never contain passwords, session tokens, one-time codes, cookies, request bodies or
  query strings; email addresses are masked.

## [2.1.3] - 2026-10-05

### Fixed
- Email Delivery (and single sign-on) settings saved with the Enable toggle off no longer
  come back as an empty form. The values are stored and shown again; only the
  password stays hidden, with a note that one is set.

## [2.1.2] - 2026-10-05

### Fixed
- `docker-compose.yml` no longer sets `BLASTA_PUBLIC_URL` to `http://127.0.0.1:<port>` by
  default. Left empty, BLASTA uses the address each request came in on, so it works at a
  domain behind a proxy (no port in the address) without any setting. Set
  `BLASTA_PUBLIC_URL` in `.env` for SSO redirects and links in emails.

## [2.1.1] - 2026-10-05

### Changed
- `docker-compose.yml` no longer forces the published port onto `127.0.0.1`; it is
  published on all interfaces. The README explains how to limit it to this machine.

## [2.1.0] - 2026-10-05

### Added
- PostgreSQL and MariaDB in Docker by editing only `.env`: `docker-compose.postgres.yml`
  and `docker-compose.mariadb.yml` add a health-checked `db` container and point blasta
  at it. Enable one with a `COMPOSE_FILE` line and set `BLASTA_DB_PASSWORD`.
- `docs/DATABASE.md` with the steps, external databases, backups and switching back.

## [2.0.0] - 2026-10-05

### Changed
- The product, the command line tool and everything technical use one name, **BLASTA**:
  the `blasta` command (built from `cmd/blasta`), `BLASTA_*` environment variables, the
  `blasta.db` database file, the `blasta-data` Docker volume, the `blasta` container and
  image, the `blasta_session` / `blasta_guest` cookies, and the Go module
  `github.com/awhadi/blasta-perftest`. The template guides and example data use the same name
  (for example Redis keys `blasta:*`).
- Settings that belong to one installation live in a git-ignored `.env` file: copy
  `.env.example`, which lists every option, and uncomment what you need. Nothing personal is
  kept in the repository or in `docker-compose.yml`.
- Saved secrets (SMTP password, SSO client secret, bot-check key) are encrypted under a key
  label that carries the new name.

## [1.2.0] - 2026-10-05

### Changed
- The product is now called **BLASTA** everywhere people read it: the page title, the
  interface text, error messages, emails (their header, subjects and the default sender
  "BLASTA by AWHADI"), the command-line help, the README and the docs, and the
  built-in template guides. The load generator now identifies itself as `BLASTA/1.0`.
- Only the name shown to people changed in this release; the command, environment
  variables and files followed in 2.0.0.

## [1.1.0] - 2026-10-05

### Changed
- The app is called **BLASTA**. The header shows the name with the smaller line
  "Performance & Load Testing Platform by AWHADI" underneath. The tagline wraps on
  tablets and is hidden on small screens, where only the name shows. The header
  separator and the Settings sidebar were widened together so they stay aligned.
- The header's menu can scroll sideways instead of widening the page on very narrow
  screens.

### Fixed
- When the data disk is full the database cannot open and blasta used to restart without
  saying why; the error now names a full disk as the likely cause and how to check it.

## [1.0.3] - 2026-10-05

### Changed
- The outline around "Sign in" / "Create account" is back: they are one joined control
  again (the borderless "Sign in" of 1.0.2 is reverted).

## [1.0.2] - 2026-10-05

### Changed
- "Sign in" no longer has an outline: it is plain text next to the orange "Create
  account", and keeps its soft orange hover.

## [1.0.1] - 2026-10-05

### Changed
- Sign in and Create account are now one joined control (one outline, one height, a soft
  half and an orange half) in the header, the free-trial bar and the "sign in to
  continue" prompt, instead of two unrelated buttons.

## [1.0.0] - 2026-10-04

The first release.

### Load testing
- Load engine with HTTP(S), TCP, WebSocket, gRPC and SQL executors; open-loop pacing
  with a linear ramp, bounded memory, HDR-histogram latency percentiles, status and
  error breakdowns, and SLO pass/fail gates (`blasta run` exits 2 on a miss).
- 77 built-in templates (1,803 jobs) for CMS and e-commerce stacks, databases,
  caches, queues, mail, LDAP, REST/SOAP, identity providers (OIDC/OAuth/SAML
  providers) and more, each with an enterprise test plan (smoke, baseline, load,
  stress, spike, soak, breakpoint, failover). A searchable Templates page and a guided
  "Use this job" dialog collect addresses and credentials with how-to guides.
- Live results over Server-Sent Events, reconnect to a running test after a reload,
  and the CPU and RAM the load generator itself used, recorded with each result.
- History: every run opens like the live view, with load settings, SLO verdict and
  server usage; JSON and CSV downloads.
- Safety: private and loopback targets blocked by default (SSRF guard), read-only SQL
  unless allowed, credentials never saved with jobs or history.

### Accounts and sign-in
- Registration, with open / approval / disabled modes and optional allowed email
  domains. With email set up, new people confirm their address from a themed email;
  without it, accounts activate at once.
- Single sign-on with any OpenID Connect provider (authorization code with PKCE,
  verified ID tokens), just-in-time accounts, administrator by email or group.
- Sign in with a one-time emailed code; password reset by emailed link; accounts made
  with SSO start without a password and can set one from My account.
- My account: name, email (confirmed from the new address), password, profile photo,
  light/dark/system theme.
- Everyone's history is private to them. Administrators manage users (activate or
  deactivate, approve, change role, reset password, add) and can delete history.
- Throttling on sign-in, registration, password reset and code requests; sessions
  stored hashed; passwords hashed with PBKDF2.
- Free trial for visitors without an account: a short time, a few small web tests
  against public addresses only, never saved. Limits are configurable.
- Bot protection with Cloudflare Turnstile or Google reCAPTCHA on sign-in,
  registration and the free trial, with an emergency off switch.

### Administration
- Settings pages in the UI: general (registration, sign-in options, public URL), free
  trial, single sign-on, email (with connection test and test email) and bot
  protection. Saved secrets are encrypted. Saves say what they did in one short line.
- Email in blasta's own look (with a dark-mode variant and plain-text part), one
  configurable sender name, and a "last delivery problem" note for the administrator.

### Deployment
- Docker image (static binary, non-root, read-only filesystem) and Docker Compose,
  Kubernetes manifests, a health check, and `/api/health` with the version.
- One database for accounts, sessions, history and settings: SQLite by default, or
  PostgreSQL / MariaDB via `BLASTA_DATABASE_URL`; migrations are automatic and the
  older `auth.json` / `runs.jsonl` files are imported on first start.
- Works behind reverse proxies (Pangolin, Traefik, nginx) and under a path such as
  `/blasta`; trusted-proxy handling for real client addresses.

### Known limits
- A running test lives in one process: run a single instance (see
  `docs/KUBERNETES.md`).
- CPU and RAM figures need Linux (cgroup v2).
- No SAML sign-in (many SAML-only providers also offer OIDC) and no built-in
  multi-factor authentication.
- The reCAPTCHA integration is covered by tests against a fake provider; Turnstile was
  also exercised against Cloudflare's published test keys.
