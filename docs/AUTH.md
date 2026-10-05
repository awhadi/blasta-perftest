# Sign-in, registration and single sign-on

BLASTA can require people to sign in. It is **on by default in Docker and
Kubernetes** and **off for a plain `blasta serve` on loopback**, where only you can
reach it. Turn it on with `BLASTA_AUTH=true` (or `blasta serve --auth`). It needs a
database to keep accounts (see [Where everything is stored](#where-everything-is-stored));
the Docker image and the Kubernetes manifests already have one.

## First run

Open BLASTA. With no accounts yet it shows **Create the administrator account**.
The first account becomes the administrator and is signed in at once. If the
server is reachable by others before you get there, set a setup token so a stranger
cannot claim it:

```bash
BLASTA_SETUP_TOKEN=some-long-random-string   # then enter it on that page
```

You can also create the first administrator from the command line:

```bash
docker compose exec -e BLASTA_NEW_PASSWORD='a long passphrase' blasta \
  blasta user create --email you@example.com --name "You" --admin
```

## Registration

Administrators switch registration on or off, and choose whether new accounts need
approval, under **Settings > General Settings** (or `BLASTA_REGISTRATION`:
`open` (default), `approval`, `closed`). What a new person goes through depends on
whether **email is set up** (Settings > Email Delivery):

| | Email set up | No email |
|---|---|---|
| **Open** | They get a themed email with a link and must **confirm their address** before the account is active. | The account is **active at once**: there is nothing to confirm with. |
| **Approval required** | They confirm their address, then wait for an administrator (who is emailed). | They wait for an administrator. |
| **Disabled** | Nobody can sign up. SSO and accounts an administrator creates still work. | Same. |

Confirmation links work once and expire after 48 hours. Someone who has not
confirmed sees why when they try to sign in, with a button to send the email again;
administrators can resend it or activate the account themselves. Under
Settings > User Management each account has an **on/off switch** to activate or
deactivate it (a deactivated person is signed out at once and cannot sign in). The very first
account (the administrator) never needs email. All emails (confirmation, password
reset, approval, change of address, test) are in BLASTA's look, with a dark-mode
variant, and a plain-text version for mail apps that do not show HTML.

`BLASTA_ALLOWED_EMAIL_DOMAINS=acme.com,acme.org` limits who can register (and who
can be created by SSO). Passwords need at least 10 characters.

## Passwords for accounts made with single sign-on

An account created by SSO starts with **no password**, and nothing can sign in to it
with one: not an empty one, not a guess, and a password-reset email will not create
one either. The person signs in with SSO, opens **My account**, and under **Password**
chooses a first one (no "current password" is asked, since there is none; it must meet
the usual rules). From then on they can sign in with either their provider or their
email address and password, and changing the password needs the current one. Setting
it signs their other sessions out. Accounts that never set one stay SSO-only.

## My account

Everyone has a **My account** page (user menu) to change their **name**, **email
address**, **password** and **photo**, and pick **Light / Dark / Match my device**.
Changing the
email needs the current password, and, with email set up, the new address must be
confirmed from a link sent there before anything changes. Accounts that sign in
only with SSO keep the address their provider gives until they set a password. Photos are shrunk in the
browser; only PNG, JPEG and WebP are accepted (never SVG), and they are checked on
the server.

## Who sees what

Everyone, administrators included, sees and controls **only their own** jobs, runs
and history. Administrators manage accounts and settings, not other people's tests.
Someone else's run answers "not found", so ids cannot be probed. When an older
install is upgraded, runs nobody owned are given to the first administrator, and
runs of accounts that no longer exist are dropped.

## Settings in the web UI

Administrators configure most of this under **Administration > Settings**, with no
restart and no environment variables:

- **Sign-up and address**: public URL, registration mode, allowed email domains.
- **Single sign-on**: the whole OpenID Connect setup below, with a **Test
  connection** button and the redirect address to copy into your provider.
- **Email**: SMTP server, port, security (STARTTLS, TLS or none), login and From
  address, with **Test configuration** (reaches the server, secures the connection and
  logs in, without saving or sending anything) and **Send a test email to me**. Mail is used for password-reset links
  and for approval notices; BLASTA works without it.
- **Free trial for visitors**: see below.

The environment variables in this page are only the **starting values**. Once an
administrator saves a section, the saved settings win; **Use defaults** forgets
them again. Secrets (the SSO client secret and the SMTP password) are encrypted in
the database with a key from `BLASTA_SECRET_KEY`, or, if that is not set, a random
key created once in `secret.key` in the data directory (owner-only). They are never
sent back to the browser. Keep the key with your backups; with several replicas
sharing one database, set the same `BLASTA_SECRET_KEY` on all of them. If the key
is lost, BLASTA reports the saved secrets as unreadable and you enter them again.

## Free trial for visitors

By default a visitor without an account lands on the **Test** page and can run a
few small tests for a short while. Templates, history and everything else show
"sign in or create an account". Defaults (all changeable in Settings): a 15 minute
trial, 3 tests, 30 seconds each, 50 requests per second, 20 connections, 20 tests
per network address per day. A trial can only run plain HTTP(S) tests, **only
against public addresses** (the private-network guard cannot be turned off), one at
a time, and is never saved to history. A day after it started, the same browser
gets a fresh trial; the per-address limit is the backstop for people who clear
their cookies. Turn it off with the switch in Settings, or `BLASTA_GUEST=false`.
Behind a proxy, set `BLASTA_TRUSTED_PROXIES` so visitors are told apart by their real
address ([PROXY.md](PROXY.md)).

## Bot protection

**Settings > Bot Protection** asks visitors to pass a quick check so automated
traffic cannot use BLASTA. Choose **Cloudflare Turnstile** or **Google reCAPTCHA v2**,
enter the provider's site key and secret key, and choose where it applies:

- **sign-in and password reset** (stops password guessing),
- **registration** (stops fake accounts; the very first administrator account is never
  challenged, or nobody could set up),
- **a visitor's first free test** (stops automated use of the free trial; passing it
  covers the rest of that trial).

It can only be turned on with a secret key the provider accepts (BLASTA asks the
provider when you save, and **Test secret key** checks one without saving), and any
failure counts as "not verified". The secret is stored encrypted, like the SMTP
password. The page is otherwise locked to itself (strict Content-Security-Policy):
the chosen provider's domains are allowed **only while a check is on**.

If a wrong or expired key ever locks everyone out, set `BLASTA_CAPTCHA_OFF=true` and
restart: the check is off whatever is saved. You can also seed it from the environment
(`BLASTA_CAPTCHA_PROVIDER`, `BLASTA_CAPTCHA_SITE_KEY`, `BLASTA_CAPTCHA_SECRET_KEY`).
For tests, Cloudflare publishes dummy keys that always pass.

## Deleting history

Ordinary users cannot delete test history: it is a record. **Administrators** get a
delete button on each run in History, a **Delete run** button on an opened run, and
**Clear all history**. These act on the administrator's own history; ticking
"Also delete every other user's history" in the Clear all dialog clears the whole
server. Tests that are still running are never deleted.

## Signing in with a one-time code

With email set up, **Settings > General Settings > Sign-in options** can let people
sign in without a password: they enter their email address, we email a **six-digit
code**, and typing it signs them in (the sign-in page shows "Email me a sign-in code").
Codes work once, expire after 10 minutes and die after five wrong guesses; asking for
codes is throttled per address and per network, and the answer is the same whether or
not the address has an account. Only a hash of the code is stored. A code also proves
an address, so it confirms an account that was waiting for email confirmation. The
bot check (if on) is asked when a code is requested. It is off by default and cannot be
turned on until email works (`BLASTA_OTP_LOGIN=true` seeds it).

## Password reset

With email set up, **Forgot your password?** on the sign-in page emails a link that
works once and expires after an hour. BLASTA never emails a password itself: the link
lets the person choose a new one, and it signs them out everywhere. Administrators can
turn this off under Sign-in options (`BLASTA_DISABLE_PASSWORD_RESET=true`). Without email, an administrator resets it
(Settings > User Management, or `blasta user reset-password`).

## Single sign-on (OpenID Connect)

Any OpenID Connect provider works: Keycloak, Okta, Microsoft Entra ID, Auth0,
Google, authentik, AD FS 2016+ and others. SAML is **not** supported; most
SAML-only providers also offer OIDC.

1. In your identity provider create a **confidential client** for BLASTA with the
   **authorization code** flow and this redirect URI (exactly):
   `https://blasta.example.com/api/auth/oidc/callback`
2. Give BLASTA the settings (put them in `.env` for Docker, or a Secret for
   Kubernetes):

```bash
BLASTA_PUBLIC_URL=https://blasta.example.com
BLASTA_OIDC_NAME=Keycloak                       # the button says "Sign in with Keycloak"
BLASTA_OIDC_ISSUER=https://idp.example.com/realms/acme
BLASTA_OIDC_CLIENT_ID=blasta
BLASTA_OIDC_CLIENT_SECRET=...                   # never commit this
```

| Setting | Purpose |
|---|---|
| `BLASTA_OIDC_DISCOVERY_URL` | Read the provider's discovery document from this address instead (when BLASTA reaches the provider on an internal address). The issuer must still match. |
| `BLASTA_OIDC_SCOPES` | Default `openid email profile`. |
| `BLASTA_OIDC_AUTO_CREATE` | Default `true`: create an account on first SSO sign-in. `false` means only people who already have an account (linked by verified email) can use SSO. |
| `BLASTA_OIDC_TRUST` | Status given to new SSO accounts: `active` (default; your provider vouches for them) or `pending` (an admin still approves). |
| `BLASTA_OIDC_ADMIN_EMAILS` | Comma-separated emails that become administrators. |
| `BLASTA_OIDC_ADMIN_GROUP` / `BLASTA_OIDC_GROUPS_CLAIM` | Members of this group (claim default `groups`) become administrators. |
| `BLASTA_SMTP_HOST`, `_PORT`, `_SECURITY`, `_USER`, `_PASSWORD`, `_FROM` | Starting values for outgoing email (Settings can change them). |
| `BLASTA_OIDC_ALLOW_INSECURE` | Allow an `http://` provider that is not on loopback. **Development only.** |

Provider notes: for Keycloak the issuer is `https://host/realms/<realm>`; Okta
`https://<org>.okta.com/oauth2/default`; Entra ID
`https://login.microsoftonline.com/<tenant>/v2.0`; Google
`https://accounts.google.com`. For group-based admin, add a groups claim to the ID
token in your provider.

An SSO sign-in is matched first by the provider's stable user id. If the email
belongs to an existing local account, the two are linked **only when the provider
says the email is verified**. Multi-factor authentication is enforced by your
identity provider, not by BLASTA.

## Managing accounts from the command line

For recovery, or when registration is closed. The running server notices the change.

```bash
blasta user list
blasta user create --email e@x.com --name "E" [--admin]
blasta user reset-password --email e@x.com
blasta user set-role --email e@x.com --role admin
```

The password comes from `BLASTA_NEW_PASSWORD` or the first line of standard input,
never from a flag, so it does not appear in process lists or shell history.
Resetting a password signs that person out everywhere.

## Security notes

- Passwords are hashed with PBKDF2-HMAC-SHA256 (600,000 iterations, random salt).
  Session tokens are stored only as hashes; the cookie is `HttpOnly`, `SameSite=Lax`
  and `Secure` over HTTPS. Sessions end after 12 hours idle or 7 days
  (`BLASTA_SESSION_IDLE`, `BLASTA_SESSION_MAX`).
- Five wrong passwords lock an account for 15 minutes, per account and per
  address. Unknown emails are throttled the same way, with the same answer and
  timing, so accounts cannot be probed. Registration is limited per address.
- SSO uses PKCE, a state bound to the browser, and a nonce. The ID token's
  signature (RS256 or ES256), issuer, audience, expiry and nonce are all verified;
  `alg: none` and HMAC tokens are refused. Providers over plain `http://` are
  refused unless on loopback.
- With SQLite, `blasta.db` and `secret.key` are created owner-only (0600).
- Always put BLASTA behind **HTTPS** (an Ingress or reverse proxy) when it is not on
  loopback, and set `BLASTA_PUBLIC_URL` to the `https://` address.
- The address used for the sign-in throttle is the connection's, not
  `X-Forwarded-For` (which anyone can forge) -- unless the connection comes from a
  proxy you listed in `BLASTA_TRUSTED_PROXIES` ([PROXY.md](PROXY.md)). Without that, all
  users behind a proxy share its address for the per-address limit; the per-account
  limit still applies.

## Where everything is stored

Accounts, sessions, run history, settings and trial counters all live in **one
database**. The default is **SQLite**: a single file, `blasta.db`, in the data
directory (`/data` in Docker and Kubernetes), with nothing else to run. For a shared
or managed database, set `BLASTA_DATABASE_URL`:

| Value | Database |
|---|---|
| empty (default) | SQLite at `<data dir>/blasta.db` |
| `sqlite:/path/to/blasta.db` | SQLite at that path |
| `postgres://user:pass@host:5432/blasta?sslmode=require` | PostgreSQL |
| `mysql://user:pass@host:3306/blasta` (or `mariadb://`) | MariaDB / MySQL (add `?tls=true` for TLS) |

The tables are created and upgraded automatically. Only run summaries are stored
(never request headers or bodies). Back up the database and `secret.key` together.
An install that used the old `auth.json` and `runs.jsonl` files is migrated on
first start, and the old files are kept next to them as `*.migrated`.

## What is not included

Email verification at sign-up, SAML, built-in multi-factor authentication, and
fine-grained roles beyond administrator and user.
