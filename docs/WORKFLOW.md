# Saving, importing, comparing and being told

How to get from "I have a request" to a repeatable test, and to hearing about it without
watching.

## My favorites

One list, **Templates > My favorites** (also in your user menu), with two kinds of card:

- **Job**: a whole setup (target, headers, body, load settings, pass/fail targets). Keep one with
  **Save as favorite** on the Jobs page or on a run in History, with the **star** on any job of a
  built-in template, or by importing a file. **Use** opens it ready to start.
- **Template**: a built-in template with your settings (your address, ports, paths) filled in. Press
  the **star** next to a template's title to keep it; it opens later with your settings in place, and
  a bar at the top has **Save settings**, **Rename** and **Remove**. Credential settings are never kept.

Click a filled star again to remove it from the list. Everything here is **private** to you (not even an
administrator sees it through the app); up to 100 of each kind. A job's setup (which may hold an
`Authorization` header or a login body) is **stored encrypted** with the site's key, the same protection
as the SMTP password; only the name, description, protocol and a "GET https://host/path" line (no query
string) are readable in the database. **Rename** and **Duplicate** are on each card, and the **×** in its top right corner removes it. Everything stays
in the app. Favorites are removed with the account.

To change a favorite job: **Use** it, change the form, press **Save changes** in the banner (or **Save as
favorite** to make a new one).

## Running several jobs

Press **Start job** as often as you like: each press starts another job and you stay on the Jobs page, so
you can change the form and start the next one. While anything is running, a **Running jobs** panel
appears on the left of every page, with each job's name, target and progress and a **×** to stop it.
Click a job there to watch it live under **Running jobs** in the menu (charts, numbers, **Stop job**, the
JSON and CSV downloads). Fold the panel to a narrow rail with the arrow at its top (it is remembered). The panel and the menu item go away when nothing is running; finished jobs are
in History. A job that finishes while you are elsewhere is announced. One person may have 5 running at
once (set `BLASTA_MAX_RUNNING` to change this). People see only their own jobs. A visitor on a free
trial has no panel: the job opens straight in Running jobs.

## Import

**Import...** on the Jobs page (or on My favorites) reads, without sending anything:

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
- **Save as favorite** on a past run fills the form from it first (history keeps no headers or
  bodies, so add those before saving).
