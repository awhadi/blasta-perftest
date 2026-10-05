# Running BLASTA on Kubernetes

Manifests are in [`deploy/k8s`](../deploy/k8s) (Kustomize). They were rendered
and linted but **not applied to a live cluster**, so try them in a scratch
namespace first.

```bash
# 1. build and push the image somewhere your cluster can pull from
docker build -t registry.example.com/blasta:0.1.0 .
docker push registry.example.com/blasta:0.1.0

# 2. point the manifests at it (edit images: in deploy/k8s/kustomization.yaml)
# 3. deploy
kubectl create namespace blasta
kubectl apply -k deploy/k8s -n blasta

# 4. open the UI
kubectl port-forward -n blasta svc/blasta 8080:80      # http://127.0.0.1:8080
#    (set BLASTA_PUBLIC_URL in deploy/k8s/deployment.yaml to match, or SSO redirects will not)
```

## What you get

| Piece | Why |
|---|---|
| Deployment, **1 replica**, `Recreate` | A run lives inside one process. More replicas would not share runs or history, and the ReadWriteOnce volume cannot be mounted by two pods at once. |
| PVC `blasta-data` (1 Gi) | Finished runs are saved to `/data`, so a restart or reschedule keeps History and report downloads. |
| Service `ClusterIP` | Reach it with a port-forward, or expose it through an Ingress **with TLS**. BLASTA now has sign-in (see [AUTH.md](AUTH.md)), so an Ingress is reasonable; never expose it with `BLASTA_AUTH=false`. |
| NetworkPolicy | Only pods in the namespace can reach the UI. Egress is open because BLASTA must reach your targets. |
| Probes, resource limits, non-root, read-only filesystem, no capabilities | Standard hardening. Raise the CPU limit for higher request rates. |

## Availability: what to expect

- If the pod dies, Kubernetes restarts it and History comes back. **A test that
  was running is lost**: it is shown as aborted after the restart.
- Restored runs have a summary and a report, but no live charts.
- This gives you a self-healing single instance, not a high-availability or
  load-sharing cluster. Spreading one test across several pods needs a
  distributed mode that does not exist yet (see [COVERAGE.md](COVERAGE.md)).
  Until then you can start the same job on several instances and add up the rates.

## Database jobs

Jobs name an environment variable that holds the DSN. Provide the values as a
Secret named `blasta-env` (optional, loaded as environment variables):

```bash
kubectl create secret generic blasta-env -n blasta \
  --from-literal=PG_DSN='postgres://user:pass@db:5432/app?sslmode=require'
kubectl rollout restart deployment/blasta -n blasta
```

## Testing services inside the cluster

The SSRF guard blocks private addresses, which includes every pod and service IP.
For in-cluster targets such as `http://my-app.staging.svc:8080`, turn off
**Block private & loopback addresses** under *Advanced limits* for that job.
Do this only for systems you own.

## Headless runs and CI gates

[`deploy/k8s/examples/run-job.yaml`](../deploy/k8s/examples/run-job.yaml) runs one
job file as a Kubernetes Job with pass/fail thresholds:

```bash
blasta run --max-error-rate 1 --max-p95 500ms job.json
```

| Exit code | Meaning |
|---|---|
| 0 | The run completed and met every threshold you set |
| 2 | The run completed but missed a threshold (the Job shows *Failed*) |
| 1 | The job could not run (bad file, unreachable executor, etc.) |

This is also the right shape for a CI step that blocks a release on a
performance regression.

## Sign-in

`deploy/k8s/deployment.yaml` turns sign-in on. Open the page and create the first
(administrator) account, or set a setup token first so nobody else can:

```bash
kubectl create secret generic blasta-env -n blasta \
  --from-literal=BLASTA_SETUP_TOKEN="$(openssl rand -hex 24)" \
  --from-literal=BLASTA_OIDC_ISSUER=https://idp.example.com/realms/acme \
  --from-literal=BLASTA_OIDC_CLIENT_ID=blasta \
  --from-literal=BLASTA_OIDC_CLIENT_SECRET=...
kubectl rollout restart deployment/blasta -n blasta
```

Accounts, sessions, history and settings live in one SQLite database (`blasta.db`, owner-only) on the same volume; or point `BLASTA_DATABASE_URL` at PostgreSQL or MariaDB. See
[AUTH.md](AUTH.md) for registration policy, SSO and recovery commands.
