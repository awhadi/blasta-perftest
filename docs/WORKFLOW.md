# Saving, importing, comparing and being told

How to get from "I have a request" to a repeatable test, and to hearing about it without
watching.

## My templates

On the **Test** page, press **Save as template** to keep the whole setup: target, headers, body,
load settings and pass/fail targets. Next time open **Templates > My templates**, press **Use**
and then **Start**.

- Templates are **private** to the person who saved them. Nobody else sees them, not even an
  administrator through the app.
- The setup (which may hold an `Authorization` header or a login body) is **stored encrypted** with
  the site's key, the same protection as the SMTP password. Only the name, description, protocol and
  a "GET https://host/path" line (no query string) are readable in the database.
- **Rename**, **Duplicate** and **Delete** are on each card. **Export** gives a job file you can run
  with `blasta run`; values of credential-like headers (Authorization, Cookie, anything with token,
  key, secret or password in its name) are left empty in the file.
- **Keeping a built-in job:** open any job under **Templates > Built-in** and press **Save a copy**.
  BLASTA asks for the details it needs (your address, credentials), fills the Test form, and offers to
  save it under a name of your own. Your copy stays exactly as you left it; it does not change when
  the built-in templates do. On the Test page the banner of a built-in job also has **Save to My
  templates**.
- Changing a template: **Use** it, change the form, press **Save changes** in the banner (or **Save as
  template** to make a new one).
- Up to 100 templates each. They are included in **My account > Download my data** (without
  credential values) and are removed with the account.

## Import

**Import...** on the Test page (or on My templates) reads, without sending anything:

| From | How to get it |
|---|---|
| a curl command | In the browser's Network tab, right-click a request, **Copy as cURL** |
| a HAR file | Network tab, **Save all as HAR**. Images, styles, scripts and fonts are left out |
| a Postman collection | **Export** the collection (v2.0 or v2.1). Collection variables are filled in where they have a value |
| an OpenAPI / Swagger document | JSON or YAML. Example values are built from the schema |
| a BLASTA job file | An exported template, or any job file |

Pick a request to fill the Test form with, or **Save all as templates**. Notes under a request flag
what to check first (placeholders to fill, authentication to add, cookies that may have expired).
The same works on the command line: `blasta import file.har --list`, then
`blasta import file.har --index 3 --out job.json`.

## Notifications (an administrator's setting)

**Settings > Notifications** (`/admin/notifications`): an administrator decides once how finished
tests are announced. People do not have to set anything up.

- **Announce finished tests**: one switch for the whole site. Tests run by anyone with an account
  are announced; visitors on the free trial are not.
- **Announce**: *only when something needs attention* (the default) or *every finished test*.
  Something needs attention when a pass/fail target is missed, when 1% or more of requests fail and the
  test has no error target, or when the test does not complete (a test its owner stops is not a
  problem).
- **Email the person who ran the test** (on by default, works once Email Delivery is set up), plus
  up to 10 extra addresses (a team address) that hear about every announced test.
- **Slack** (an incoming webhook; also Mattermost), **Microsoft Teams** (a Workflows webhook) and
  **any webhook** (BLASTA posts JSON): one shared channel each. The message says who started the test.
- **Send a test** posts a sample to everything that is saved.

Messages carry the headline numbers, who started the test and a link, never headers, bodies or
credentials. The channel addresses are secrets (anyone with one can post there), so they are stored
encrypted and shown only as a host. They must be `https://`, and addresses on private networks are
refused so a webhook cannot be aimed at internal services; to allow them (an internal Mattermost, say)
set `BLASTA_ALLOW_PRIVATE_WEBHOOKS=true`. The privacy page mentions the emails and the shared channel
when they are on.

The JSON a webhook receives:

```json
{"event": "run.finished", "url": "https://blasta.example.com/history/run_...",
 "run": {"id": "run_...", "job": "Orders API", "startedBy": "pat@example.com", "target": "https://...",
         "state": "finished", "needsAttention": true, "problems": ["p95 340 ms is over the 250 ms target"],
         "total": 1200, "errors": 30, "errorRatePercent": 2.5, "avgRps": 40,
         "p50Ms": 12, "p95Ms": 340, "p99Ms": 1250, "durationSeconds": 30}}
```

## Baselines and comparing runs

On a finished run in **History**, **Set as baseline** marks it as the run later runs of that test
are compared with (one per test; a test is the same protocol, target and name). Open any later run
of the test and it is compared with the baseline automatically; or pick any other run of the same
test. Add limits to turn the comparison into a pass or fail:

- p95 and p99 may be slower by N%
- the error rate may rise by N points
- throughput may fall by N%

Changes under 5% are shown as "about the same": two runs of one test never match exactly.

### In CI

Save a run's summary once, then gate later runs on it:

```bash
blasta run job.json --save-report baseline.json          # on the release you trust; keep the file
blasta run job.json --baseline baseline.json \
  --max-latency-regression 15 --max-error-increase 1 --max-throughput-drop 10
```

The second command prints the comparison and exits with status 2 if a limit is broken (the same
exit code as a missed SLO), so it fails the pipeline. Keep `baseline.json` as a build artifact, or
in the repository, and refresh it when you accept a new normal.

## Small things

- **Run again** on the live results repeats the test with the same settings.
- **Download job file** (Test page) saves the current form as a job file for the command line.
- **Save as template** on a past run fills the form from it first (history keeps no headers or
  bodies, so add those before saving).
