# Menina

Menina is a self-hosted application platform written in Go. Rent a VPS, install
Menina, point a registry domain at it, and deploy applications by pushing
container images — Menina reconciles the workloads into k3s and routes public
hostnames through its own reverse proxy.

> The product and its CLI are **Menina** (`menina`). Earlier drafts used "lab".

## Guiding principle

- **Go (Menina)** decides *what* should run, from configuration and deployment policy.
- **SQLite** preserves operational state and pending work.
- **Kubernetes (k3s)** decides *how* to run it and maintains the requested workload state.
- **Zot** stores images; push events trigger Go to reconcile the relevant workloads.

Menina owns the higher-level deployment model; k3s and Zot remain responsible
for orchestration and the image registry.

## Architecture

| Component | Responsibility |
| --- | --- |
| `menina` CLI | Setup, projects, apps, environments, deployments, logs, diagnostics |
| SQLite + config files | Desired configuration, release history, routing metadata, job state |
| Config watcher | Detects and validates file changes; single writer |
| Registry listener | Receives Zot events and durably queues releases |
| Go reverse proxy | Routes public hostnames to Kubernetes Services |
| Deployment engine | Uses the Kubernetes API to manage workloads |
| k3s + Zot | Container orchestration, image registry, application runtime |

One executable, distinct modes:

```bash
menina app deploy api --project my-platform --environment production
menina serve   # control plane: watcher + registry listener + reconciliation
menina proxy   # reverse proxy (separate process, independent restarts)
```

## Install

The installer **installs; it does not provision**. It downloads a verified
native binary, creates the `menina` system user, installs shell completion and
the systemd unit templates, then stops so you can inspect the binary and run
provisioning explicitly:

```bash
curl -fsSL https://menina.moureau.dev/install.sh | sudo sh
sudo menina setup
sudo menina deploy
```

Installing Menina does **not** require Docker: k3s ships its own containerd
runtime.

**Prerequisites for a clean Ubuntu Server VPS**: `amd64` or `arm64`, systemd,
`curl`, network access to `get.k3s.io` / GitHub Releases / `ghcr.io` /
Let's Encrypt, and ports `80` and `443` reachable from the internet (plus
`9000` reachable from the cluster for registry webhooks).

**Privileges**: only `install`, `setup` and `deploy` need root — they create the
`menina` system user, install k3s, deploy Zot, write systemd units and bind
`:80`/`:443`. The long-running `menina serve`/`menina proxy` services run as the
unprivileged `menina` user. Day-to-day CLI commands run as **any user in the
`menina` group** (the installer adds `$SUDO_USER` automatically; otherwise
`sudo usermod -aG menina $USER`, then log out and back in). The configuration
directory is group-writable (`root:menina`, setgid) and the control socket is
group-accessible, so project/app management, `status`, `routes`, `logs`,
`doctor`, `app deploy`, `app rollback` and `registry credential` need no sudo.

### Fresh install walkthrough (ending in HTTPS)

```bash
# 1. Install (root)
curl -fsSL https://menina.moureau.dev/install.sh | sudo sh

# 2. Configure (root). Point a wildcard-less DNS record at the VPS first
#    (e.g. *.example.com or explicit hosts -> VPS IP). Choose:
#      - install k3s with Traefik disabled
#      - install Zot, set registry.example.com, generate credentials
#      - Networking: Public VPS ingress (optionally automate DNS with a
#        Cloudflare API token)
#      - TLS: terminate in Menina -> ACME / Let's Encrypt, enter an email
sudo menina setup

# 3. Provision k3s, storage, Zot, the control plane and the proxy.
#    This waits for the k3s API, deploys Zot, wires the registry route,
#    creates the service user's kubeconfig, and starts the systemd units.
sudo menina deploy

# 4. Retrieve the registry push credential (no sudo; user in the menina group)
menina registry credential

# 5. Define an application (config files are group-writable; no sudo)
menina project create my-app --namespace my-app
menina environment create production --project my-app
menina app create api --project my-app \
  --repository registry.example.com/my-app/api --port 8080

# 6. Push an image to the registry (from your build machine, not the VPS)
docker login registry.example.com -u menina
docker build -t registry.example.com/my-app/api:production .
docker push registry.example.com/my-app/api:production

# 7. Deploy it (the push also auto-deploys; this forces it explicitly)
menina app deploy api --project my-app --environment production

# 8. Add exposure so the app has a route and a certificate
#    In apps/api.yml set workload port; in environments/production.yml:
#      apps:
#        api:
#          enabled: true
#          image: { tag: production }
#          exposure:
#            enabled: true
#            hosts: [api.example.com]
#    then redeploy:
menina app deploy api --project my-app --environment production
```

`api.example.com` now terminates TLS at the VPS with a Let's Encrypt
certificate (issued proactively once the route is published) and proxies to the
app's Kubernetes Service. Verify with `menina doctor` and `menina routes`.

`menina setup`/`menina deploy` create the registry and platform; they do **not**
create an application for you. A deployable app needs a project/app/environment
definition and an image pushed to the registry.

Other runtime behavior worth knowing:

- `registry.retention.enabled: false` disables the generated Zot retention policy.
- `security.enforceProdPolicy: true` (default) makes `app deploy`/`app rollback`
  require `--yes` (and the control plane reject unconfirmed requests) for the
  `production` environment. `security.trustedProxies` is the single trusted-proxy
  list the proxy uses; `setup` prompts for it.
- `menina backup` writes a consistent `VACUUM INTO` copy of the SQLite database to
  `<data-dir>/backups/`.
- `menina deploy platform` refuses to run when the registry is enabled but the
  Zot Service is absent, telling you to run `menina deploy infra` first.
- Cloudflare Tunnel mode installs the local proxy origin (the tunnel needs an
  origin service to forward to).
- For a machine with no published release, install a locally built binary with
  `MENINA_LOCAL_BINARY=/path/to/menina sh scripts/install.sh`.

## CLI

The full command surface — including the interactive shortcuts (`menina create`,
bare `menina app` / `project` / `environment`) — is documented in the
[docs](docs/README.md) ([CLI reference](docs/CLI.md)). Quick start:

```bash
menina create my-app          # create a project; asks if you use multiple environments
menina app --project my-app   # pick an app → Deploy / Scale / Logs / ...
menina app scale api --project my-app --min 2 --max 10              # baseline (all envs)
menina app scale api --project my-app -e production --replicas 4    # production only
```

## Configuration

```text
/etc/menina/
├── config.yml
└── projects/
    └── my-platform/
        ├── project.yml
        ├── apps/{frontend,api,worker}.yml
        └── environments/{development,staging,production}.yml

/var/lib/menina/
├── menina.db
├── registry/
├── releases/
└── backups/
```

Human-editable YAML is the source of desired configuration; SQLite stores
indexed/resolved configuration, deployment jobs, image digests, route mappings,
release history and observed status. `menina serve` is the **single writer** for
SQLite; the proxy receives versioned routing snapshots over an authenticated
Unix socket and never writes the database.

### Config merge rules (documented explicitly)

The resolver combines a shared app definition with environment overrides:

| Field | Rule |
| --- | --- |
| `enabled` | Environment override wins; defaults to `true` |
| `image.repository` | **Immutable** — an environment may not change it |
| `image.tag` | Environment override wins; otherwise the platform `defaults.imageTagStrategy` (default: the environment name) |
| `workload` | Field-wise: non-zero override fields replace app fields |
| `resources` | Field-wise merge (non-empty override fields win) |
| `exposure` | Environment must **opt in**; `enabled` is taken from the environment. Public exposure always requires environment-level opt-in |
| `storage` | Environment override replaces the app's storage block |
| `health` | Environment override replaces the app's health block |
| `replicas` | Environment override wins; otherwise the app value; otherwise platform default. Accepts a fixed count (`3`) or a range (`{min: 2, max: 10, targetCPU: 70}`), realized as a HorizontalPodAutoscaler |
| `env` | Merged; environment keys win |

Scaling (`replicas`) accepts either a fixed count or a range:

```yaml
replicas: 3                            # fixed
replicas: { min: 2, max: 10 }          # HorizontalPodAutoscaler
replicas: { min: 2, max: 10, targetCPU: 70 }   # targetCPU defaults to 70
```

A range creates (and keeps in sync) a `HorizontalPodAutoscaler` targeting the
app's Deployment with a CPU-utilization metric; a fixed count removes any stale
HPA. HPA requires the metrics API (k3s ships metrics-server by default).

Change it without touching YAML:

```bash
menina app scale api --project my-app --replicas 4      # fixed
menina app scale api --project my-app --min 2 --max 12  # autoscaling
menina app scale api --project my-app --reset           # inherit the default
```

`menina app create` is interactive when run from a terminal (prompting for
project, repository, port and scaling); pass `--yes` plus flags for scripts. The
platform defaults asked during `menina setup` (namespace, replicas, CPU, memory)
fill in for any app that does not set them.

The resolved configuration is validated as a whole before any cluster change.
If a config file is invalid, the last valid resolved snapshot is retained and
the error is reported rather than deploying a partial update.

## Registry credentials

`menina setup` generates a random push credential for Zot by default. The
password is written with `0600` permissions and referenced from `config.yml`
only as a secret reference (`file:/etc/menina/secrets/registry-password`) — the
configuration never contains plaintext credentials. `setup` also generates the
webhook authentication token that Zot presents to the Menina event listener, so
the listener is not anonymously writable.

Show the current credential (for `docker login` / CI):

```bash
menina registry credential
```

Rotate it — generates a new random password, updates `config.yml`, updates the
Zot `htpasswd` secret, rewrites `/etc/rancher/k3s/registries.yaml`, and rolls
managed workloads so they pull with the new credential:

```bash
menina registry credential --regenerate
```

`menina deploy` reconciles the same material idempotently, so a failed partial
rotation is repaired by re-running deploy. Credentials are resolved from secret
references (`env:`, `file:`, `literal:`) and are never written into app
definitions or logged.

## Deployment

```bash
menina deploy            # orchestrate infra then platform
menina deploy infra      # k3s, storage, registries.yaml, Zot
menina deploy platform   # systemd services, DNS/tunnel wiring
menina deploy --dry-run  # print the plan
```

`menina deploy` is idempotent. When Menina owns public ingress it installs k3s
with the packaged Traefik ingress controller and ServiceLB disabled, and asserts
`:80`/`:443` are free; if an ingress controller already owns those ports, Menina
integrates rather than evicting it.

### Ingress ownership and host ports

- **Menina-owned ingress (default):** k3s is installed with Traefik disabled and
  Menina's proxy owns `:80`/`:443`.
- **External ingress owns the ports:** Menina integrates with the existing
  controller instead of competing for the sockets.
- Conflicting listeners are detected and explained, never silently modified.

### Host proxy → Kubernetes Services

The host proxy forwards to Kubernetes **ClusterIPs** (watched from `Service`
objects) so Kubernetes keeps ownership of backend selection, readiness
filtering and balancing. `menina serve` resolves each exposed app to its
Service ClusterIP:port and publishes it in the routing snapshot. The registry
domain is routed the same way to Zot's Service.

On the supported single-node k3s setup, kube-proxy programs DNAT rules in the
host/node network namespace, so a host process (the proxy) can reach a
`ClusterIP:port` directly. Important: **reachability must be tested with TCP,
not ICMP** — kube-proxy handles TCP/UDP, so `ping <ClusterIP>` fails even when
routing works. `menina doctor` performs this TCP check per route.

Routing is robust across pod replacement and rollouts because the ClusterIP is
stable and kube-proxy updates endpoints automatically; during a rollout there
can be a brief window with no ready endpoints (a `502`), after which routing
recovers without any route change. The routing snapshot is **rebuilt on a
timer** (in addition to config changes and startup), so if a Service was missing
at start-up or its ClusterIP changed, routes are refreshed and republished
automatically.

Reproduce/verify the full path with a real k3s:

```bash
scripts/k3s-net-e2e.sh   # starts k3s in Docker and runs the host->ClusterIP e2e test inside the node netns
```

The proxy unit runs in the host network namespace (no `PrivateNetwork`), which
is required for ClusterIP routing.

### Networking modes and TLS layers

| Mode | Client → public TLS | Origin | Proxy → backend |
| --- | --- | --- | --- |
| Public VPS ingress (Menina terminates TLS) | Menina proxy `:443` (ACME) | — | plain HTTP to `ClusterIP:port` |
| Public VPS ingress (TLS upstream) | Cloudflare/load balancer | proxy `:80` | plain HTTP to `ClusterIP:port` |
| Cloudflare Tunnel | Cloudflare | `cloudflared` → local origin (`http://127.0.0.1:80` by default) | plain HTTP to `ClusterIP:port` |

In **Cloudflare Tunnel** mode Cloudflare terminates public TLS, so Menina must
**not** also run ACME at the origin; `setup` forces `tls.terminate: false` and
`menina deploy` refuses an ACME+tunnel combination. The configured
`networking.cloudflare.originUrl`/`originProto` must match whether the proxy
serves plain HTTP or TLS, and this is validated. `cloudflared` itself is
installed and configured by the operator (Menina prints the origin to use), not
by `menina deploy`.

## Automatic deployment on push

CI pushes an image; Zot emits an event; Menina **durably persists the event and
jobs (committed to SQLite) before acknowledging the webhook**. Events and jobs
carry stable deduplication keys and re-applying a desired digest is a no-op. The
tag is resolved to an immutable digest when the job is processed so the platform
converges on the latest published image. Periodic reconciliation re-resolves
configured tags and detects drift, comparing the resolved digest against the
workload's image before triggering a rollout.

Zot's events extension publishes **CloudEvents** (the `zotregistry.image.updated`
event carries `name`/`reference`/`digest`), delivered over the HTTP sink with an
optional `Authorization: Bearer` header that Menina's listener verifies. This
extension exists since Zot **v2.1.3**, so Menina pins v2.1.22; the generated Zot
config (`extensions.events.sinks`, `storage.retention.policies`) is validated
against that schema. Menina's listener accepts both CloudEvents (binary and
structured) and the legacy OCI `events` array.


## Deploy notifications

Menina can POST a generic, provider-agnostic JSON event to an operator-supplied
receiver whenever something deploys. Menina does **not** speak Slack/Discord/
Sentry directly; point it at your own relay, which fans out to whatever you use.

```yaml
# config.yml (platform default)
webhooks:
  deploy:
    enabled: true
    urlRef: "env:MENINA_DEPLOY_WEBHOOK"          # or file:/etc/menina/secrets/deploy-webhook
    secretRef: "file:/etc/menina/secrets/deploy-webhook-signing"   # optional HMAC
    events: [deploy.succeeded, deploy.failed, deploy.rolled_back]

# project.yml (per-project override)
webhooks:
  deploy:
    urlRef: "file:/etc/menina/secrets/my-platform-hook"
```

The POST body is stable and self-describing:

```json
{
  "event": "deploy.succeeded",
  "action": "deploy",
  "project": "my-platform",
  "environment": "production",
  "app": "api",
  "repository": "registry.example.com/my-platform/api",
  "tag": "production",
  "digest": "sha256:…",
  "image": "registry.example.com/my-platform/api@sha256:…",
  "status": "succeeded",
  "actor": "registry",
  "message": "my-platform/production/api succeeded (sha256:…)",
  "timestamp": "2026-10-14T12:00:00Z"
}
```

Headers: `X-Menina-Event`, `X-Menina-Delivery` (unique per delivery), and, when a
signing secret is set, `X-Menina-Signature: sha256=<HMAC-SHA256(body)>`. URLs and
secrets are secret references (`file:`/`env:`), never plaintext in `config.yml`.

Delivery is best-effort (a short timeout with a few retries, never blocking the
deployment). `menina notify list` shows configured webhooks and `menina notify
test [--project p]` sends a synthetic event. By default only definitive outcomes
(`deploy.succeeded`, `deploy.failed` after retries are exhausted, and
`deploy.rolled_back`) are sent; `deploy.queued` is available via the `events` list.

The control plane also exposes Prometheus metrics at `/metrics` on the events
listener (`menina_snapshot_version`, `menina_routes`, `menina_jobs{status="…"}`).

## Reverse proxy

The proxy forwards to Kubernetes Services over plain HTTP. Capability status:

| Capability | Status | Notes |
| --- | --- | --- |
| WebSockets (Upgrade, full-duplex) | implemented, e2e tested | `httputil.ReverseProxy` hijack path; client→proxy over HTTP/1.1 |
| SSE / chunked streaming | implemented, e2e tested | `FlushInterval = -1` (immediate flush) on every route by default |
| Large uploads / downloads | implemented, e2e tested | streamed, not buffered; per-route `exposure.maxBodyBytes` limit |
| HTTP/2 (client→proxy) | implemented | negotiated via ALPN on the TLS listener |
| gRPC / h2c (plaintext HTTP/2 upstream) | implemented, e2e tested | set `exposure.h2c: true`; trailers preserved via the h2c transport |
| gRPC over TLS upstream | not supported | backends are plaintext ClusterIPs; use `exposure.h2c` for gRPC |
| Per-route timeouts | implemented | `exposure.timeouts.read` (response-header), `.idle` (conn), `.write` (request deadline) |
| Compression (gzip) | implemented, tested | per-route `exposure.compression`; skipped for 1xx/204/304 |
| Forwarded headers | implemented, tested | `X-Forwarded-For/Proto/Host`, `X-Real-IP`; trusted proxies only |
| HTTP→HTTPS redirect | implemented, tested | honors `tls.httpRedirect` and `exposure.redirectHttp` |
| Connection reuse | implemented | cached transports keyed by H2C + timeouts |

The client-facing server sets `ReadHeaderTimeout`/`IdleTimeout` but **no**
`ReadTimeout`/`WriteTimeout`, so long-lived streams are not cut off; timeouts are
per-route. h2c is an upstream (backend) mode only — the listener itself serves
HTTP/1.1 and HTTP/2-over-TLS, not cleartext h2. The protocol matrix is covered by
end-to-end tests in `internal/proxy`.

## TLS

TLS termination is a first-class toggle. Modes: ACME/Let's Encrypt, manual
certificates, cert-manager on k3s, or none (TLS handled upstream by Cloudflare,
a tunnel or a load balancer).

With **ACME/Let's Encrypt** (`tls.mode: acme`), Menina terminates TLS itself and
obtains a certificate **automatically for every hostname that has a route**:
exposing an app (`exposure.enabled: true` with `hosts`) is enough — Menina feeds
the live routing table into the ACME host policy, so each proxied app gets its
own certificate. Certificates for route hosts are also **proactively acquired**
(warmed) as soon as routes are published and refreshed on a timer, so the first
visitor never waits for issuance and idle hosts still renew. Certificates are
cached under `tls.acme.storageDir` and renewed automatically (both HTTP-01 on
`:80` and TLS-ALPN-01 on `:443`). Anything under `networking.baseDomains` is
also eligible.

Prerequisites: the public DNS name must resolve to the VPS and ports `:80`/`:443`
must be reachable. When `networking.cloudflare.apiTokenSecretRef` and a public IP
(`networking.publicIp`, or detected automatically) are available, `menina deploy`
creates the `A` records for the base domains, the registry domain, **and every
resolved application exposure host**; it refuses to overwrite a name that already
has a record of a different type. `menina doctor` validates the ACME directory
reachability, the certificate directory, the webhook listener (reachable and
authenticated), and the served certificate for every exposed hostname (a
hostname unreachable from inside the VPS is reported as a warning, not a
failure).

Wildcards (`*.example.com`) are **not** supported: routing and ACME use
individual hostnames, and wildcard certificates would require DNS-01. Menina
rejects wildcard hosts with a clear configuration error.

`tls.mode: manual` serves certificates you place in `tls.certDir` named
`<host>.crt` / `<host>.key`; the directory is re-read periodically so renewed
files are picked up without a restart. `cert-manager` certificates are not
consumed automatically by the host proxy yet, so `setup` steers that choice to
manual mode.

HTTP→HTTPS redirects honor `tls.httpRedirect` and per-route `exposure.redirectHttp`,
and only trust `X-Forwarded-Proto` from explicitly configured trusted proxies —
a client cannot spoof it to bypass the redirect. If Cloudflare is proxying the
record (orange cloud), Cloudflare owns client-facing TLS: use Cloudflare Tunnel
mode or `tls.mode: none` instead of Menina ACME.

Forwarded headers are trusted only from explicitly configured proxies; `X-Forwarded-*`
is never trusted merely because it is present.

## Docker

The production model is native (binary + systemd). A container image and a
compose stack are provided for development, demos and CI:

```bash
docker compose up --build
```

The `init` service runs `menina setup --non-interactive` once; `serve` exposes
the control socket and the registry listener on `:9000`; `proxy` publishes
`:80`/`:443`; and a Zot container is included for local image pushes.

Build the image directly:

```bash
make docker            # or: docker build -t menina:dev .
```

## Security and recovery

- `menina serve` and `menina proxy` run as a dedicated unprivileged user, with
  systemd hardening (`NoNewPrivileges`, `ProtectSystem=full`, `ProtectHome`,
  `ProtectKernel*`, `ProtectControlGroups`, `PrivateDevices`, `PrivateTmp`,
  `RestrictNamespaces`, `RestrictSUIDSGID`, `LockPersonality`, and a minimal
  `RestrictAddressFamilies`). Only the proxy is granted `CAP_NET_BIND_SERVICE`.
- The registry webhook is bearer-token authenticated and **fails closed**: if
  notifications are enabled without a valid token, `menina serve` refuses to
  start; if notifications are disabled, no listener is started. Forwarded
  headers are trusted only from configured proxies; untrusted
  `X-Forwarded-Host`/`Forwarded` are stripped. The proxy routes only configured
  hostnames (unknown hosts get 404) and never dials a client-controlled
  destination.
- **Kubernetes access is a scoped identity.** `menina deploy` creates a
  ServiceAccount (`menina-system/menina`), a `menina-platform` ClusterRole with
  only the namespaces/nodes(read)/services/configmaps/secrets/PVCs/pods/pods-log/
  deployments verbs it needs, and a long-lived token; the service kubeconfig uses
  that token only. Menina never falls back to the admin kubeconfig as the service
  credential. Verified against real k3s (`scripts/rbac-e2e.sh`).
- `menina deploy` propagates systemd install/enable/start failures (nonzero exit,
  no success message), `menina app deploy` waits for the rollout and reports
  succeeded/failed, and Zot readiness is verified before deploy reports success.
  `menina upgrade` verifies the artifact checksum, executes the staged binary
  before replacing it (atomic, same-directory rename with a `.bak`), and reports
  that services must be restarted and migrations run on next start.
- CI registry credentials are separate from cluster credentials.
- On SIGTERM the control plane stops accepting work, re-queues in-progress
  deployments and flushes state. When Kubernetes is unavailable it stays up,
  keeps queuing events and retries with backoff. After a database restore,
  Menina reconciles against the live cluster and registry rather than trusting
  the restored database blindly.

## Development

```bash
make build    # build ./bin/menina
make test     # go test ./...
make race     # go test -race ./...
make vet      # go vet ./...
```

The database uses text primary keys generated with
`lower(hex(randomblob(12)))` and `created_at`/`updated_at`/`deleted_at`
columns defaulting to `unixepoch()`, with supporting indexes on every lookup
path.

## License

Licensed under the [Apache License, Version 2.0](LICENSE).

Copyright 2026 MOUREAU DEVELOPMENT LTDA.

