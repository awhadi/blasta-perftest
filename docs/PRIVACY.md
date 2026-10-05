# Privacy, cookies and data protection

This explains what a BLASTA site stores, what the controls in **Settings > Privacy & Cookies** do,
and what is still the operator's job. It is not legal advice: what the law requires depends on
where your visitors are and what you do with their data.

## What BLASTA stores

| Data | Why | How long |
|---|---|---|
| Account: email, name, role, optional photo, password as a salted hash | Sign-in | Until the account is deleted |
| Sessions: hashed token, time, network address, browser | Stay signed in; show and end sessions | Until they expire (7 days by default) |
| Test history: target, settings, results (never headers or request bodies) | Review and compare | Until deleted by the person or the account |
| Free-trial use by visitors: a random id and counts, network address per day | Trial limits, abuse control | About 3 days |
| Network addresses in the server log (`docker compose logs`) | Operate and secure the service | As long as your log retention keeps them |

## Cookies

BLASTA's own cookies are all **strictly necessary**, which most laws allow without consent:
`blasta_session` (sign-in), `blasta_guest` (free-trial limit, 2 days) and `blasta_oidc_state` (during
single sign-on, 10 minutes). The light/dark choice and the running test are kept in the browser's
own storage. If you add a consent banner BLASTA also sets `blasta_consent` (12 months) to remember
the choice. The bot check (Cloudflare Turnstile or Google reCAPTCHA) may set its own cookies for
security. **Analytics** (Settings > Analytics) is the optional part, and the one that can need
consent.

## The controls

**Settings > Privacy & Cookies** (`/admin/privacy`):

- **Cookie consent**, one mode for everyone:
  - *Ask first (opt-in)*: analytics does not load until a visitor accepts. The usual rule in the EU,
    the UK, Brazil and several other countries. This is the default.
  - *Tell people, let them opt out*: analytics runs, visitors are told and can switch it off. Fits
    places such as California, which also expect Global Privacy Control to be honoured (leave
    "Respect Do Not Track" on in Analytics).
  - *Notice only*: a banner that explains the essential cookies, with no choice.
  - *No banner*.
  The banner appears only when there is something to ask about (analytics is on), or in notice mode.
  A "Cookie settings" button lets people change their mind; declining after accepting reloads the
  page so what was running stops.
- **Privacy page** (`/privacy`): generated from your settings and what is switched on (the bot
  check, single sign-on, the free trial, analytics). Add who runs the site, a contact for requests,
  a link to your own policy, and extra paragraphs. The banner links to your policy if you set one.
- **People's rights**: everyone can download their data (account, sessions, test history) from **My
  account**. Deleting their own account and history is on by default; switch it off if you must
  keep records (people can still ask you).

## What is still yours

- Choose the mode that fits your visitors. BLASTA does not detect where a visitor is, so one mode
  applies to everyone: pick the strictest that applies to you.
- BLASTA keeps the visitor's choice in a cookie only. It does not keep a server-side log of
  consents, which some laws or regulators expect.
- Contracts with your analytics, email and hosting providers, a legal basis for what you do, how
  long you keep logs, breach notification, and answering requests are yours.
- Counting visitors, a privacy notice and consent are legal questions for your jurisdiction. Ask a
  professional if you are unsure.
