# Analytics

An administrator can count visits to a BLASTA site with their own analytics service:
**Settings > Analytics** (`/admin/analytics`). It is off until you turn it on, and switching it
off again removes it at once; what you entered stays saved.

## Services

| Service | You enter |
|---|---|
| Google Analytics 4 | Measurement ID, like `G-ABC123XYZ9` |
| Google Tag Manager | Container ID, like `GTM-ABC123` |
| Plausible | Your site's domain; optionally your own Plausible address |
| Umami | Website ID; optionally your own script address |
| Matomo | Site ID (a number) and your Matomo address |
| Cloudflare Web Analytics | Beacon token |
| Another service | The address of its script (https) |

## How it works

BLASTA's pages allow scripts only from BLASTA itself, so a tag cannot be pasted into the page.
Instead BLASTA serves a short script of its own at `/analytics.js`, built from the fixed
templates above and your validated values (never free-form code), and that script loads the
service. The page's security policy then lets through only the addresses that service needs,
and only while it is on. If a service talks to more hosts than it needs by default (tags
loaded through Google Tag Manager, for example), list them under **Other addresses**.

## Privacy

- **Respect Do Not Track** (on by default): nothing loads for people whose browser sends Do Not
  Track or Global Privacy Control.
- **Also count people who are signed in** (off by default): by default only visitors are
  counted, never the people using BLASTA.
- Pages whose address carries a one-time token (password reset and email confirmation links,
  `token=`, `code=`, `state=`) are never counted, so a secret cannot reach a third party.
- Counting visitors can require their consent in your country. That, and a privacy notice, are
  yours to provide; BLASTA only loads the service you chose.

The settings are stored in the database with the other admin settings. Sign-in must be on
(`BLASTA_AUTH=true`), as for every admin setting.
