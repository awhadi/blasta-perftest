# Behind a reverse proxy, and under a path

BLASTA works behind Pangolin, Traefik, nginx, Caddy, an Ingress or any similar proxy,
at the root of a host (`https://blasta.example.com/`) or under a path
(`https://example.com/blasta/`, `http://localhost:8080/blasta/`).

## The Public URL

**Settings > General Settings > Public URL** (or `BLASTA_PUBLIC_URL`) is the address
people type to reach BLASTA *from outside*, the one your proxy publishes. BLASTA uses it for:

- the **single sign-on redirect address** you register at your identity provider
  (`<public URL>/api/auth/oidc/callback`, shown with a Copy button when you add a provider);
- the links in **emails** (password reset, approval notices);
- deciding that cookies are **HTTPS-only** (`https://` means `Secure`);
- accepting browser requests whose `Origin` is that address.

It does **not** change where BLASTA listens. If you leave it empty, BLASTA works out
the address of each request from the `Host`, `X-Forwarded-Host`, `X-Forwarded-Proto` and
`X-Forwarded-Prefix` headers, which is fine for simple setups, but set it for SSO
and email, where a fixed address matters. If BLASTA lives under a path, include it:
`https://example.com/blasta`.

## Under a path

The page and every request it makes use relative addresses, so the same build works
anywhere. Two cases:

- **The proxy strips the prefix** (Traefik `StripPrefix`, Pangolin path rules that
  rewrite): nothing to configure on BLASTA. Open the address **with a trailing slash**
  (`/blasta/`); without it, BLASTA's page adds the slash itself.
- **The proxy passes the prefix through**, or you open BLASTA directly:
  set `BLASTA_BASE_PATH=/blasta` (or put the path in the Public URL). BLASTA then
  answers at `/blasta/...` and still at `/...`.

`BLASTA_BASE_PATH` accepts letters, digits and `- . _ ~ /`.

## Who is the client? (`BLASTA_TRUSTED_PROXIES`)

Sign-in throttling and the free-trial limits count people by network address. Behind
a proxy every request arrives from the proxy, so BLASTA would see one visitor. Tell it
which proxies to believe:

```bash
BLASTA_TRUSTED_PROXIES=private            # loopback + 10/8 + 172.16/12 + 192.168/16 (Docker, most clusters)
BLASTA_TRUSTED_PROXIES=172.18.0.0/16,10.1.2.3
```

Only connections from those addresses may set `X-Forwarded-For` (the rightmost
address that is not itself a trusted proxy is taken as the client). From anywhere
else the header is ignored, so it cannot be used to dodge the limits. Leave it empty
when nothing sits in front of BLASTA.

## What the proxy must send

- `Host` (original or rewritten) and ideally `X-Forwarded-Host`, `X-Forwarded-Proto`
  (and `X-Forwarded-Prefix` if it strips a path). These are the defaults of Traefik,
  Pangolin, Caddy and most Ingress controllers; for nginx add
  `proxy_set_header X-Forwarded-Host $host; proxy_set_header X-Forwarded-Proto $scheme;`.
- **No response buffering** for `/api/runs/*/stream` (live results use Server-Sent Events).
  BLASTA sends `X-Accel-Buffering: no` for nginx; other proxies usually stream by default.
- A request timeout longer than your longest test is not needed (the page reconnects),
  but do not cut idle streaming connections under about 30 seconds.

## Docker

```yaml
environment:
  BLASTA_PUBLIC_URL: https://blasta.example.com
  BLASTA_TRUSTED_PROXIES: private
  # BLASTA_BASE_PATH: /blasta        # only if the proxy does not strip the path
```
