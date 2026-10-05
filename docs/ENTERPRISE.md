# Enterprise test plans

Every template includes an **enterprise test plan**: jobs whose ids start with
`ent-`, built from that template's most representative request. They follow the
sequence performance teams use before a release or a capacity review.

| Step | Job | What it answers | Gate (default) |
|---|---|---|---|
| 01 | smoke | Is the target up and the job configured correctly? 1 req/s for 30 s | 0 errors |
| 02 | baseline | What is the uncontended latency? 20% load, 5 min | 0.5% errors, latency targets |
| 03 | average load | Do we meet the SLO at normal traffic? 10 min | 1% errors, latency targets |
| 04 | peak load | Do we meet it at 2x? 10 min | 2% errors, latency x2 |
| 05 | stress | Where and how does it degrade? Ramp to 4x over 10 min | none (observe) |
| 06 | spike | Does it survive 10x in 10 s? Autoscaling, queues, load shedding | 5% errors |
| 07 | recovery | Does it return to baseline after the spike? Run right after 06 | same as 03 |
| 08 | soak | Leaks and slow decay? 1 hour at 60% | 0.5% errors, latency targets |
| 09 | breakpoint | What is the ceiling? Ramp to 20x over 20 min | none (find it) |
| 10 | resilience window | What does a failover or deploy cost? 15 min at average | 1% errors |

Jobs also carry **extras** that match how production traffic really behaves:

- **Web (`ent-x-…`):** no keep-alive (a new TCP and TLS handshake per request),
  crawler traffic, large cookies (header size limits), uptime-monitor HEAD storms.
- **Databases:** connection-pool exhaustion.
- **TCP, WebSocket, gRPC, brokers:** connection storms (fleet reconnects).
- **Real-world scenarios per family:** Monday-9am login storm and token-expiry herd
  for OIDC and SAML, signing-key rotation herd, credential-stuffing resilience,
  gateway introspection peak, flash sale and viral post for CMS and shops, hot key
  and cache-miss storms for Redis and Memcached, hot item, deep pagination and
  write peak for REST, WSDL refetch herd for SOAP, device-fleet reconnect for MQTT.

Hosted identity providers (Okta, Auth0, Entra ID, Cognito) get a reduced plan:
smoke, baseline, average, peak and a 30-minute soak. Spike, stress and breakpoint
against a shared service would breach its rate limits and terms.

## Running a plan

In the web UI: **Start from a template**, render, open **Enterprise test plan**
and load a step. SLO targets are filled in, and the result is marked
*SLO met* or *SLO missed*.

From the CLI, in order, with gates:

```bash
scripts/run-plan.sh wordpress --url https://staging.example.com --set post=hello-world
scripts/run-plan.sh redis --set host=redis.staging --steps 01,02,03,06,07
scripts/run-plan.sh keycloak --url https://kc.staging --set realm=app --extras
```

Check the wiring first with a dry run that is 20 times shorter (about 12 minutes
instead of 4 hours):

```bash
scripts/run-plan.sh wordpress --url https://staging.example.com --time-scale 0.05
```

The script stops if the smoke test fails, records every gate, prints a summary and
exits 2 if any gate was missed. A single job works the same way:

```bash
blasta run job.json            # exit 0 met, 2 missed, 1 could not run
blasta run --max-p95 300ms job.json      # a flag overrides the job's own SLO
blasta run --ignore-slo job.json
```

## Make the targets yours

The shipped numbers are **defaults, not your SLO**:

- **Rate.** "Average load" uses the reference request's rate (often 25 req/s, chosen
  to be safe on staging). Raise it to your measured production peak-hour rate.
- **Latency.** p95/p99 targets default to 1 s / 2 s for HTTP, 250 ms / 1 s for SQL,
  50 ms / 200 ms for TCP services, and so on. Replace them with the figures in
  your SLA. A job file's `slo` block is `{"maxErrorRate": 1, "maxP95": 500000000,
  "maxP99": 1000000000}` (percent, then nanoseconds).
- **Environment.** Run on a production-like environment. Results from a laptop
  database say nothing about production.

## Rules of the road

- Run steps in order. A failed smoke or baseline means later results are noise.
- Run 07 immediately after 06, and watch the time series, not only the totals.
- For 10, you cause the failure (kill a pod, fail over the database, deploy). BLASTA
  only measures what the client sees.
- Watch the system under test (CPU, memory, connections, queue depth, GC) beside
  BLASTA. Latency tells you that something is wrong, not what.
- Jobs marked write or mutating create data on every request. Staging only.
- One request type per job. A realistic mix of several requests running together
  is not possible yet; run the individual jobs side by side from separate
  terminals as an approximation (see [COVERAGE.md](COVERAGE.md)).
