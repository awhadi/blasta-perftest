# Changelog

All notable changes to blasta are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/): a fix bumps the last number, a new
feature the middle one, a breaking change the first. The current version is the
`Version` constant in `internal/version/version.go`; it is shown bottom right in the
page for signed-in people, by `blasta version`, and in `/api/health`.

## [2.10.0] - 2026-10-07

### Added
- **My templates**: save the whole setup of a test (target, headers, body, load settings, pass/fail
  targets) and run it again later, from **Templates > My templates**. Private to each person; the
  setup is stored encrypted with the site's key, so credentials in it are protected like the SMTP
  password. Rename, duplicate, delete, export as a job file (credential-like header values are left
  out), up to 100 each. Included in "Download my data" and removed with the account.
- **Import** from a curl command (browsers' "Copy as cURL"), a HAR file, a Postman collection, an
  OpenAPI / Swagger document (JSON or YAML) or a BLASTA job file: pick a request to fill the Test form
  with, or save them all as templates. Also `blasta import <file>`.
- **Notifications**: be told when your tests finish by email, Slack, Microsoft Teams or any webhook,
  every time or only when something needs attention, with a "send a test" button. Webhook addresses
  are stored encrypted and private-network addresses are refused (`BLASTA_ALLOW_PRIVATE_WEBHOOKS`).
- **Baselines and comparing runs**: mark a run as the baseline for its test; later runs are compared
  with it automatically, or compare any two runs, with limits that turn it into a pass or fail
  (p95/p99 slower by N%, error rate up by N points, throughput down by N%).
- **CI gates**: `blasta run --save-report` and `--baseline` with `--max-latency-regression`,
  `--max-error-increase` and `--max-throughput-drop` (exit status 2 on a miss).
- Small additions: **Run again** on the live results, **Download job file** on the Test page,
  **Save as template** from a past run, a "baseline" tag in the run list.
- See [docs/WORKFLOW.md](docs/WORKFLOW.md).

### Changed
- The database schema moves to version 7 (two new tables). A copy of the database from before is
  not needed: the step only adds tables.
- A curl import with a JSON body and no `Content-Type` is sent as `application/json`.
- New dependency: `gopkg.in/yaml.v3` (for OpenAPI documents written as YAML).

## [2.9.0] - 2026-10-05

### Added
- **Settings > Privacy & Cookies**: how optional cookies are handled and what the site says about
  your data. A cookie consent banner with four modes (ask first, opt-out, notice only, none),
  a "Cookie settings" button to change a choice, and analytics that stays off until consent where
  the mode requires it. See [docs/PRIVACY.md](docs/PRIVACY.md).
- A privacy page at `/privacy`, generated from the site's real settings: what is stored and for how
  long, a table of the cookies actually in use (including the bot check, single sign-on, the free
  trial and analytics when they are on), the visitor's choices, and your own contact, controller
  name, policy link and extra text. It is linked from the sign-in page and the banner.
- People can **download their data** (account, sessions, test history) and **delete their own
  account and history** from My account, after confirming with their password (or their email for
  single sign-on). Wrong attempts are throttled and the last administrator cannot be deleted. An
  administrator can switch self-service deletion off.

### Changed
- Pages load `/consent.js` (when there is analytics or a notice) instead of loading analytics
  directly, so analytics waits for the visitor's choice. `blasta_consent` remembers it for 12 months.

## [2.8.0] - 2026-10-05

### Added
- **Settings > Analytics** for administrators: count visits with your own Google Analytics 4,
  Google Tag Manager, Plausible, Umami, Matomo, Cloudflare Web Analytics, or another service's
  script. One switch turns it on or off (the settings stay saved), and "Reset to Default" forgets
  them. See [docs/ANALYTICS.md](docs/ANALYTICS.md).
- It is built for the strict security policy: BLASTA serves its own short `/analytics.js`, made
  from fixed templates and checked values (an id is never pasted as code), and the policy lets
  through only the hosts the chosen service needs, only while it is on.
- Privacy defaults: Do Not Track and Global Privacy Control are respected, signed-in people are
  not counted (an option allows it), and addresses with a one-time token (password reset, email
  confirmation) are never counted.

## [2.7.0] - 2026-10-05

### Added
- Every test starts with a `User-Agent` row in the Headers section, filled with `BLASTA/<version>`
  (it used to say `BLASTA/1.0` whatever the version), so the systems being tested can tell its
  traffic. It is an ordinary header: change or remove it if you need to. A template job that
  brings its own agent (the crawler test sends a Googlebot one) keeps it, with `BLASTA/<version>`
  added after it, still editable. When a job sends no User-Agent at all, the HTTP, WebSocket and
  gRPC tests introduce themselves as `BLASTA/<version>`.

### Changed
- Closing the template banner (the x) also puts away the live results panel. A test that is
  still running keeps running; only the panel is hidden, and the next test shows it again.

## [2.6.1] - 2026-10-05

### Fixed
- Text at the top of a page could appear small for a moment and then grow when the web fonts
  arrived (most visible over a slow connection or a proxy). The fonts now use
  `font-display: block`: they are preloaded and small, so the text appears once, already at
  its final size, instead of being drawn in a fallback font first.

## [2.6.0] - 2026-10-05

### Added
- Real addresses for every page of the app: `/templates`, `/templates/auth0`, `/history/<run>`,
  `/login`, `/admin/users` and so on, instead of `/#/templates/auth0`. The browser's back and
  forward buttons, new tabs and bookmarks work as before, and old `#` addresses (including the
  links already sent in emails, and single sign-on returns) are converted automatically.
- The server renders the text of each template page itself: its own title, description,
  canonical address, structured data and the full page (what it covers, how to run it, every
  job, questions and answers), inside the app's page. Search engines and AI crawlers read it
  without running scripts; people whose scripts run get the app. `/templates` lists all 77.
- `sitemap.xml` and `llms.txt` list the real template addresses again, generated from the
  catalogue so a new template appears by itself.
- Sign-in, account, history and other private pages answer with `noindex`.

### Changed
- The security policy now allows the page's own `<base>` (`base-uri 'self'`, was `'none'`). It
  is what lets the same files load from any depth and under any path a proxy mounts the app at,
  and it still refuses a base on another site.
- The page title follows the page you are on.
- Wrong addresses and unknown template ids answer 404 (with `noindex`).

## [2.5.2] - 2026-10-05

### Added
- `sitemap.xml` now lists every template (`/#/templates/<id>`) next to the home page. It is
  generated from the built-in catalogue, so a new template appears without any extra step.
  Note that search engines ignore the part after `#`, so these entries do not by themselves
  make individual templates searchable.

## [2.5.1] - 2026-10-05

### Fixed
- Safari (and Android browsers) coloured the browser bar and page edges orange, because the
  `theme-color` added for search metadata used the accent colour. It now matches the
  header (white in the light theme, dark grey in the dark theme) and follows the theme you
  pick in the app, not only the device's.

## [2.5.0] - 2026-10-05

### Removed
- The public `/templates/` pages (the index and one page per template, added in 2.3.0) and
  everything only they used: their entries in the sitemap and `llms.txt`, their structured
  data and the link from the no-JavaScript fallback. Templates are browsed in the app
  (`#/templates/<id>`), which visitors can read without an account. The home page metadata,
  `robots.txt`, a one-page `sitemap.xml`, `llms.txt` (now a plain list of the templates by
  category) and the favicon, compression and caching fixes stay. `/templates/...` answers 404.

## [2.4.4] - 2026-10-05

### Removed
- The footer links under the app ("All load testing templates", "Plain-text overview",
  "Sitemap") added in 2.4.1. The pages stay reachable for search engines and AI through
  `sitemap.xml`, `robots.txt`, `llms.txt` and the no-JavaScript fallback, so the app looks
  as it did before. The version badge (bottom right when signed in) was never changed.

## [2.4.3] - 2026-10-05

### Changed
- Structured data brought in line with search-engine guidelines: the organisation now has a
  square 512 px logo (`/logo.png`) and links to the GitHub project; the application states
  its version; every template article names its image, author, publisher (with logo),
  category and main page. A test checks all pages: valid JSON, no empty values, absolute
  URLs and the required properties. Nothing is invented: there are no ratings, prices or
  dates, because the product has none to state.

## [2.4.2] - 2026-10-05

### Fixed
- The home page had six `<h1>` headings, one per hidden app view, and a few empty headings
  that scripts fill in later. It now has a single `<h1>`; the other views use `<h2>` with
  the level-1 heading role, so screen readers see no change and looks are unchanged. The
  empty headings have placeholder text. A test keeps it that way.
- The home page description now mentions run history and is about 155 characters.

## [2.4.1] - 2026-10-05

### Fixed
- `/favicon.ico` returned the app's HTML page; it now serves a real icon (`favicon.png`,
  also linked from every page).
- Unknown addresses returned `200` (a "soft 404" that search engines may index); they now
  answer `404` with `X-Robots-Tag: noindex`, still showing the app so a mistyped address
  lands somewhere useful. `/templates` redirects to `/templates/` instead of duplicating it.
- Pages, scripts, styles and the sitemap are compressed with gzip for clients that ask for
  it; the API and its live run stream are not touched.
- Static files carry an `ETag` and answer `304 Not Modified` when unchanged (an upgrade is
  still picked up at once), and the fonts are cached for a year.

### Added
- A small footer in the app links to `/templates/`, `llms.txt` and the sitemap, so the
  template pages are reachable by a link from the home page.

## [2.4.0] - 2026-10-05

### Added
- Every template now has a description that says what it covers and what it shows, written
  for each of the 77 templates from its real jobs (for example which pages, endpoints or
  protocols it exercises, and what to watch for). It is shown on the template cards and at
  the top of the template page in the app, is searchable, is returned by the API and
  `blasta preset show`, and is the basis of each template's search-engine page, its meta
  description and `llms.txt`. A test requires one for every template.

## [2.3.2] - 2026-10-05

### Changed
- Search metadata rewritten to follow search-engine guidance: plain titles under 60
  characters ("WordPress Load Testing Template | BLASTA"), descriptions of 70 to 160
  characters that say what the page is, and no marketing claims ("free", "self-hosted").
  The keywords tag (ignored by search engines) is gone, and the structured data now
  describes the organisation, site and application without offers.
- Every template page now has its own content, generated from that template's real jobs:
  an overview (what it tests, how many jobs, scenarios, enterprise plan stages, pass/fail
  targets), a how-to-run section naming the settings it needs, a safety summary, and a
  question-and-answer section with matching FAQ structured data. The template index has
  category sections with jump links.

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
