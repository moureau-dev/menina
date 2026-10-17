# Menina

Menina is a self-hosted application platform written in Go. Rent a VPS, install
Menina, point a registry domain at it, and deploy applications by pushing
container images — Menina reconciles the workloads into k3s and routes public
hostnames through its own reverse proxy.

> The product and its CLI are **Menina** (`menina`). Earlier drafts used "lab".

## Documentation

Detailed docs live in [`docs/`](docs/README.md):

| Doc | Covers |
| --- | --- |
| [Client CLI](docs/CLIENT-CLI.md) | Cross-platform client, manifests, apply/build/push, CI |
| [CLI reference](docs/CLI.md) | Every command, interactive flows, environment/scaling model |
| [Configuration](docs/CONFIGURATION.md) | File layout, merge rules, scaling, deploy notifications |
| [Proxy](docs/PROXY.md) | Routing, TLS, networking modes, caching, security, protocols |
| [Kubernetes (k3s)](docs/KUBERNETES.md) | Provisioning, namespaces, RBAC, workloads, reconciliation |
| [Registry](docs/REGISTRY.md) | Bundled Zot: auth, push, digest pinning, retention, auto-deploy |
| [Operations](docs/OPERATIONS.md) | Privileges, security, backups, upgrades, Docker, development |
| [Changelog](CHANGELOG.md) | Notable changes per release |

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

### Client (laptop or CI)

The client builds on Linux, macOS and Windows and only needs the manifest
commands (`init`, `validate`, `apply`, `build`, `push`). It is included in the
host binary, or install the standalone client:

```bash
# Linux / macOS
curl -fsSL https://menina.moureau.dev/install-client.sh | sh

# Windows (PowerShell)
irm https://menina.moureau.dev/install-client.ps1 | iex
```

The installers verify the release checksum and install the command as `menina`
(they fall back to `menina-client` if a host `menina` is already present). On
Windows it is added to the user-level `PATH`. `menina apply --host user@vps`
sends the manifest over SSH; build/push use the local Docker daemon.
`menina version --check` compares with the latest release and `menina upgrade`
performs a checksum-verified self-update.

**Prerequisites for a clean Ubuntu Server VPS**: `amd64` or `arm64`, systemd,
`curl`, network access to `get.k3s.io` / GitHub Releases / `ghcr.io` /
Let's Encrypt, and ports `80` and `443` reachable from the internet (plus `9000`
reachable from the cluster for registry webhooks). Only `install`, `setup` and
`deploy` need root; see [Operations](docs/OPERATIONS.md#privileges--users) for
the privilege model and the `menina` group.

### Fresh install walkthrough (ending in HTTPS)

```bash
# 1. Install (root)
curl -fsSL https://menina.moureau.dev/install.sh | sudo sh

# 2. Configure (root). Point DNS at the VPS first. Choose k3s (Traefik off),
#    install Zot + registry domain, networking mode, and TLS (ACME).
sudo menina setup

# 3. Provision k3s, storage, Zot, the control plane and the proxy
sudo menina deploy

# 4. Registry push credential (no sudo; user in the menina group)
menina registry credential

# 5. Define an application (config files are group-writable; no sudo)
menina create my-app            # or: project create / environment create / app create

# 6. Push an image (from your build machine)
docker login registry.example.com -u menina
docker build -t registry.example.com/my-app/api:production .
docker push registry.example.com/my-app/api:production

# 7. Deploy it (the push also auto-deploys; this forces it explicitly)
menina app deploy api --project my-app --environment production
```

Add `exposure` (hosts) to an environment to give the app a route and a
Let's Encrypt certificate; then `menina app deploy` again. `menina setup` /
`menina deploy` create the registry and platform, but **not** an application —
a deployable app needs a project/app/environment definition and a pushed image.
Verify with `menina doctor` and `menina routes`.

## CLI

The full command surface — including the interactive shortcuts (`menina create`,
bare `menina app` / `project` / `environment`) — is in the
[CLI reference](docs/CLI.md). Quick start:

```bash
menina create my-app          # create a project; asks if you use multiple environments
menina app --project my-app   # pick an app → Deploy / Scale / Logs / ...
menina app scale api --project my-app --min 2 --max 10              # baseline (all envs)
menina app scale api --project my-app -e production --replicas 4    # production only
```

## Configuration

Desired state is human-editable YAML under `/etc/menina`; SQLite holds resolved
config, jobs, digests, routes and release history (`menina serve` is the single
writer). The resolver merges each app with per-environment overrides and
validates the whole config before any cluster change. See
[Configuration](docs/CONFIGURATION.md) for the layout, the field-by-field merge
rules, scaling, deploy notifications and the config reference.

## Deployment & operations

`menina deploy` is idempotent and orchestrates infra (k3s, storage,
`registries.yaml`, Zot) then platform services; `menina deploy --dry-run` prints
the plan. It installs k3s with Traefik and ServiceLB disabled and asserts
`:80`/`:443` are free, integrating with an existing ingress instead of evicting
it.

- Cluster internals (namespaces, scoped RBAC, workloads, reconciliation):
  [Kubernetes](docs/KUBERNETES.md).
- Edge (routing, ClusterIP forwarding, TLS, networking modes, protocols):
  [Proxy](docs/PROXY.md).
- Registry (Zot, credentials, digest pinning, retention, push-triggered
  deploys): [Registry](docs/REGISTRY.md).
- Privileges, systemd hardening, fail-closed webhook, scoped kube identity,
  backups (`menina backup`), upgrades (`menina upgrade`), Docker and dev:
  [Operations](docs/OPERATIONS.md).

## Deploy notifications

Menina can POST a generic, provider-agnostic JSON event to your own receiver on
deploy outcomes (it does not speak Slack/Discord/Sentry directly). Configure
`webhooks.deploy` and inspect it with `menina notify list|test`. The payload,
HMAC signature headers, and Prometheus `/metrics` are documented in
[Configuration → Deploy notifications](docs/CONFIGURATION.md#deploy-notifications).

## Development

```bash
make build    # build ./bin/menina
make test     # go test ./...
make race     # go test -race ./...
make vet      # go vet ./...
```

See [Operations → Development](docs/OPERATIONS.md#development) for the database
schema notes and the Docker compose stack.

## License

Licensed under the [Apache License, Version 2.0](LICENSE).

Copyright 2026 MOUREAU DEVELOPMENT LTDA.
