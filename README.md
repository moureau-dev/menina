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
native binary, installs shell completion and the systemd unit templates, then
stops so you can inspect the binary and run provisioning explicitly:

```bash
curl -fsSL https://menina.moureau.dev/install.sh | sudo sh
menina setup
menina deploy
```

Installing Menina does **not** require Docker: k3s ships its own containerd
runtime.

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
| `replicas` | Environment override wins; otherwise the app value; otherwise platform default |
| `env` | Merged; environment keys win |

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
Service ClusterIP:port and publishes it in the routing snapshot. Host-to-
ClusterIP connectivity is a tested prerequisite of the supported single-node
k3s configuration.

## Automatic deployment on push

CI pushes an image; Zot emits a notification; Menina **durably persists the
event and jobs (committed to SQLite) before acknowledging the webhook**. Events
and jobs carry stable deduplication keys and re-applying a desired digest is a
no-op. The tag is resolved to an immutable digest when the job is processed so
the platform converges on the latest published image. Periodic reconciliation
re-resolves configured tags and detects drift, comparing the resolved digest
against the workload's image before triggering a rollout.

## Reverse proxy

The proxy behaves like a proper reverse proxy out of the box: WebSockets,
HTTP/2 and gRPC (h2c optional), SSE/chunked streaming with immediate flush,
large streamed uploads, per-route timeouts, compression, custom headers,
HTTP→HTTPS redirect when Menina owns TLS, and correct forwarded headers from
trusted upstreams only. The protocol matrix is covered by end-to-end tests in
`internal/proxy`.

## TLS

TLS termination is a first-class toggle. Modes: ACME/Let's Encrypt, manual
certificates, cert-manager on k3s, or none (TLS handled upstream by Cloudflare,
a tunnel or a load balancer). Forwarded headers are trusted only from
explicitly configured proxies; `X-Forwarded-*` is never trusted merely because
it is present.

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

- `menina serve` and `menina proxy` run as a dedicated unprivileged user; only
  host provisioning requires elevation, and privileged port binding is granted
  narrowly (`CAP_NET_BIND_SERVICE`).
- Kubernetes access uses a scoped service identity; CI registry credentials are
  separate from cluster credentials.
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

