# BLASTA

**Load testing with a web UI and a CLI.** One small static binary (or a Docker
container), no telemetry. BLASTA generates load against HTTP, SQL, TCP, gRPC and
WebSocket targets with bounded memory and honest open-loop timing, and comes with 77
ready-made templates (1,803 jobs) for the systems people actually run.

Current version: see [CHANGELOG.md](CHANGELOG.md) (the version also shows bottom right
in the page when you are signed in, and in `blasta version`).

## What you get

- **Test anything common:** websites and APIs, databases, caches, queues, identity
  providers (OIDC/SAML), LDAP, mail servers, REST/SOAP; with SLO pass/fail gates.
- **Live results and history:** streaming charts, the CPU/RAM the test itself used,
  and every past run to reopen, compare and download.
- **Teams:** accounts with registration, single sign-on (OpenID Connect), email
  confirmation, one-time sign-in codes, password reset, bot protection, and a free
  trial for visitors. Everyone's history is private to them.
- **Run it anywhere:** Docker Compose, Kubernetes, behind a reverse proxy, under a
  path; SQLite by default or PostgreSQL / MariaDB.
- **Administered from the UI:** registration, SSO, email, bot protection and the
  free-trial limits are all settings pages; secrets are stored encrypted.

## Quick start

```bash
docker compose up -d --build        # http://127.0.0.1:8080
```

The first account you create becomes the administrator. Or without Docker:

```bash
make build
./blasta serve                       # http://127.0.0.1:8080 (no sign-in on loopback)
./blasta run examples/http_simple.json   # headless, with SLO exit codes
```

## Documentation

| | |
|---|---|
| [CHANGELOG.md](CHANGELOG.md) | what changed in each version |
| [docs/AUTH.md](docs/AUTH.md) | accounts, registration, SSO, email, bot protection, the database, security notes |
| [docs/PROXY.md](docs/PROXY.md) | reverse proxies (Pangolin, Traefik, nginx) and running under a path |
| [docs/KUBERNETES.md](docs/KUBERNETES.md) | manifests and what "available" means for BLASTA |
| [docs/ENTERPRISE.md](docs/ENTERPRISE.md) | the enterprise test plan on every template |
| [docs/COVERAGE.md](docs/COVERAGE.md) | what is and is not covered |

## Search engines and AI

The app is a single page, which crawlers cannot read, so `/` carries full metadata (title,
description, Open Graph and Twitter cards, canonical address, structured data) and a
no-JavaScript summary, and `/robots.txt`, `/sitemap.xml` and `/llms.txt` (a plain-text
description for AI assistants) describe the site. Addresses in them follow
`BLASTA_PUBLIC_URL`, or the address the request came in on, so set that to your real
domain. The API stays disallowed for crawlers. The templates are browsed in the app.

## Presets

BLASTA ships 77 built-in job templates (1,803 jobs), each with an enterprise test plan (smoke, baseline, load, stress, spike, soak, breakpoint, failover window, with SLO gates; see [docs/ENTERPRISE.md](docs/ENTERPRISE.md)). Besides the CMS, shop and
database stacks (WordPress, WooCommerce, Joomla, Drupal, Magento, PostgreSQL,
MySQL and more) they cover the standard test shapes (smoke, load, stress, spike,
soak), caching and CDN behaviour, rate limits, authenticated APIs, Keycloak/OIDC,
Laravel, Django, Rails, Spring Boot, Elasticsearch, Socket.IO, Grafana and
Prometheus, plus OIDC/OAuth and SAML identity providers (Keycloak, Okta, Auth0,
Entra ID, AD FS, Shibboleth and more), LDAP, Redis, Memcached, RabbitMQ, MQTT,
mail servers, REST and SOAP services. See [docs/COVERAGE.md](docs/COVERAGE.md)
for what is covered and what is not. They are embedded in the binary, so there is nothing to
install and the templates cannot drift from the engine.

```bash
blasta presets                      # list the catalogue by category
blasta presets --category CMS       # one category
blasta preset show wordpress        # full definition, including variables
blasta preset new wordpress --url https://staging.example.com
blasta preset new wordpress --url https://staging.example.com --job single-post
blasta preset new mysql-read --set table=wp_posts --out wp-db.json
```

`preset new` writes real job files you can read and edit before running:

```bash
blasta preset new wordpress --url https://staging.example.com
#   wordpress-jobs/wordpress-front.json
#   wordpress-jobs/wordpress-single-post.json
#   wordpress-jobs/wordpress-rest-posts.json
#   wordpress-jobs/wordpress-admin.json  [MUTATING]
#   ...
blasta run wordpress-jobs/wordpress-single-post.json
```

`--url` adapts to the preset. For a WebSocket preset an `https://` site URL
becomes `wss://`; for TCP and gRPC presets it is treated as `host:port`. Every
generated job is validated before anything is written, so a template mistake is
reported instead of producing a job that cannot connect.

Any variable can be overridden, and common aliases are accepted:

```bash
blasta preset new wordpress --set site=https://staging.example.com --set slug=my-post
```

Variables still holding a stand-in default (`example.com`, a sample slug) are
reported as warnings, because a job aimed at `example.com` fails confusingly.

### Credentials

A credential variable defaults to an environment reference rather than a real
secret, so nothing sensitive is ever written to disk:

```bash
export WP_ADMIN_COOKIE='wordpress_logged_in_...'   # Joomla: JOOMLA_ADMIN_COOKIE, Drupal: DRUPAL_ADMIN_COOKIE
blasta run wordpress-jobs/wordpress-admin.json
```

`${VAR}` expansion happens in the CLI only. The API deliberately does not expand
environment variables, so a web caller cannot read the server's environment back
out.

### In the UI

The **Templates** page lists every template with search (by system, protocol or
what a job tests, such as *spike* or *login storm*) and category filters. Open
one and press **Use this job**. A dialog asks for everything that job needs: your
address and other values, the page path when the job has a fixed one, and any
credentials such as a WordPress admin cookie. Each field has a **How do I get
this?** guide. Credentials are typed once, go straight into the form, and are not
saved with the template or in History; the job file itself keeps `${NAME}` for the
CLI, which reads them from environment variables. You then land on the Test page
with the form filled in, ready to review and start. **Edit details** on the banner
reopens the dialog with your answers to change the address, path or a credential;
your rate, duration and other load settings are left as you set them.

### Leaving and coming back

The test you started is remembered (by its id only, never the job or any
credential). If you reload the page or return later, BLASTA **reconnects to the
running test** and keeps streaming it, or shows the finished result with its SLO
verdict and server resources. While a test runs, the **Test** tab shows a pulsing
dot wherever you are, and the **BLASTA** logo always takes you back to the start page.

### History

The History page lists your runs with their average CPU and RAM and an SLO met or
missed tag. History is private: you see only your own tests. Administrators can
delete runs and clear the history. Click a run (or press Enter on it) to open it the way it looked live:
verdict, stat cards, response-time percentiles, status codes and errors, the
requests-per-second and latency charts, and the server's CPU and RAM with the
**averages first**. It also shows what the live view cannot: when it ran, how long,
the target, the load settings (rate, connections, ramp), the SLO targets it was
judged against, and why it ended if it was stopped. **Use these settings** puts the
target and load settings back into the Test form; headers and bodies are never
saved with history, so they are not restored. JSON and CSV downloads are on the
run's page. Runs are saved to the data volume, so they survive restarts; runs saved
by older versions open without charts or load settings.

### What the load test cost the server

While a test runs, the live results show the CPU and RAM that BLASTA itself is using
(the container it runs in), with current and peak values and two small charts. When
the run ends, the average and peak are **saved with the result**: they appear in
History, in the JSON report (`resources`) and in the CSV (`server_*` rows). If CPU
reaches about 85% the result is flagged, because a saturated load generator makes
the target look slower than it is. The server samples once a second, only while a
run is active, and keeps a bounded series. Linux only (it reads cgroup v2).

### Fonts

The UI is typeset in Space Grotesk (wordmark and headings), DM Sans (text) and
JetBrains Mono (numbers and code). The files live in `internal/ui/dist/fonts`
and are served by BLASTA itself, so nothing is loaded from a third party. All
three are under the SIL Open Font License (see `fonts/OFL-NOTICE.txt`).

## Sign-in

In Docker and Kubernetes BLASTA asks people to sign in. The first account becomes the
administrator. After that, administrators choose in **Settings** whether people can
register, whether new accounts need approval, and how people sign in: password,
one-time emailed code, or **single sign-on** with any OpenID Connect provider
(Keycloak, Okta, Entra ID, Auth0, Google...). With email set up, new people confirm
their address first; without it, accounts activate at once. A bot check
(Cloudflare Turnstile or Google reCAPTCHA) can protect sign-in, registration and the
free trial. Visitors without an account can try a few small tests for a short while.
Plain `blasta serve` on loopback stays sign-in free. Everything is explained in
[docs/AUTH.md](docs/AUTH.md).

## Docker

```bash
docker compose up -d --build        # http://127.0.0.1:8080
BLASTA_PORT=9090 docker compose up -d   # use another host port
docker compose logs -f blasta
docker compose down
```

**Logs.** `docker compose logs -f blasta` shows start-up (with the effective settings),
sign-ins and refusals, account and settings changes, runs, mail checks and failed requests.
For more detail set `BLASTA_LOG_LEVEL=debug` in `.env` and run `docker compose up -d`: every
request is then logged (method, path, status, time, client address). `BLASTA_LOG_FORMAT=json`
gives JSON lines for a log collector. Passwords, tokens, cookies, request bodies and query
strings are never logged, and email addresses are masked.

The image is a ~23 MB static binary on Alpine, runs as a non-root user with a
read-only filesystem and all capabilities dropped, and has a health check.

- **Network access.** `docker-compose.yml` publishes the port on all interfaces.
  Sign-in is on, but a load generator is a request amplifier: put it behind HTTPS
  (see [docs/PROXY.md](docs/PROXY.md)), and if you want it reachable from this
  machine only, change the port line to `"127.0.0.1:${BLASTA_PORT:-8080}:8080"` or
  use a firewall. Behind a domain, set `BLASTA_PUBLIC_URL` in `.env`
  (for example `https://blasta.example.com`, no port); empty works too.
- **Testing services on your machine.** From inside the container `localhost`
  is the container itself. Use `http://host.docker.internal:PORT` and turn off
  *Block private & loopback addresses* under Advanced limits.
- **Database jobs.** Put the DSN variables in `.env` (git-ignored); the
  UI only ever takes the variable *name*.
- **Everything is saved** in one database, `blasta.db` on the `blasta-data` volume
  (`/data`): accounts, run history (each person sees only their own) and the
  settings an administrator changes in the UI (single sign-on, email, free-trial
  limits). It survives restarts and rebuilds. Use PostgreSQL or MariaDB instead by
  editing `.env` only: see [docs/DATABASE.md](docs/DATABASE.md). Outside Docker it is opt-in: `blasta serve --data-dir DIR`
  or `BLASTA_DATA_DIR`. A test that is running when the process dies is lost. See
  [docs/AUTH.md](docs/AUTH.md).
- **Behind a reverse proxy or under a path** (Pangolin, Traefik, nginx, `/blasta`):
  see [docs/PROXY.md](docs/PROXY.md).
- **Visitors without an account** land on the Test page and can run a few small,
  time-limited tests. They can also browse and read every template, but using a job
  and history ask them to sign in or register.
- Without Compose: `docker build -t blasta .` then
  `docker run -d -p 8080:8080 blasta`.

## Kubernetes

Manifests, an in-cluster Job example and an honest account of what "available"
means for a single-instance tool are in [docs/KUBERNETES.md](docs/KUBERNETES.md).

## CLI

```bash
blasta serve [flags]      start the web UI
blasta run <job.json>     run a job headlessly (job SLO or --max-error-rate/--max-p95/--max-p99: exit 2 on a miss; --time-scale for dry runs)
scripts/run-plan.sh <preset> ...   run a template's enterprise plan in order
blasta check <job.json>   validate a job
blasta presets            list built-in presets
blasta preset show <id>   print a preset as JSON
blasta preset new <id>    generate runnable job files
```

## Job files

Durations are nanoseconds. `rps: 0` runs closed-loop (as fast as each worker
completes); a positive `rps` is open-loop and paced by a scheduler, with
`ramp` increasing the rate linearly. Concurrency is capped by `maxWorkers` and
`queueSize` bounds the backlog, so memory stays flat regardless of duration.

```json
{
  "name": "front page",
  "executor": "http",
  "target": { "url": "https://example.com/" },
  "concurrency": 20,
  "rps": 25,
  "duration": 60000000000,
  "timeout": 15000000000
}
```

## Safety

- The server binds loopback by default. `-allow-remote` is required for anything
  else. Sign-in is on in Docker and Kubernetes; without it (plain `blasta serve`) there
  is no authentication, so keep it on loopback.
- `blockPrivate` (on by default) refuses loopback, link-local, metadata and
  RFC1918 targets, which blocks the usual SSRF pivots.
- SQL statements are read-only unless `db.allowWrite` is set. Writable CTEs,
  multi-statement input and `EXPLAIN ANALYZE DELETE` are refused.
- Database DSNs are referenced by env var name (`db.dsnEnv`), never inline.
- Preset jobs that write are tagged, and `preset new` marks them.

## Releases

BLASTA uses [semantic versioning](https://semver.org/). The version is the constant in
`internal/version/version.go`: bump the last number for a fix, the middle one for a
new feature, the first for a breaking change. For each release:

1. bump `Version` in `internal/version/version.go`;
2. add a `## [x.y.z] - date` entry at the top of [CHANGELOG.md](CHANGELOG.md);
3. update this README and the `docs/` page if behaviour changed;
4. run `go vet ./... && go test ./...`, then commit and tag `vX.Y.Z`.

## Development

```bash
make test          # go test ./...
make race          # go test -race ./...
make vet           # go vet plus a gofmt check
make build         # static binary

python3 hack/genpresets.py   # regenerate internal/presets/data from the generator
```

Presets are generated rather than hand-maintained. Edit `hack/genpresets.py`,
then regenerate; `internal/presets` tests assert that every preset renders into
job files the engine accepts.