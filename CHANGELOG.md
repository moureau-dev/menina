# Changelog

All notable changes to Menina are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.0.1] - 2026-10-14

The first release: a self-hosted application platform in a single Go binary
that deploys container images to k3s and routes public hostnames through its own
reverse proxy.

### Added

#### Platform
- One `menina` binary with distinct modes: `setup`, `deploy` (infra/platform,
  `--dry-run`), `serve` (control plane), and `proxy` (edge).
- Declarative configuration: `config.yml` plus per-project `project.yml`,
  `apps/*.yml` and `environments/*.yml`, merged field-by-field into a resolved
  model and validated as a whole before any cluster change.
- SQLite state (release history, routing, jobs, observed status) with
  `menina serve` as the single writer; the proxy receives versioned routing
  snapshots over an authenticated Unix socket.
- Config file watcher, periodic reconciliation, and tag re-resolution with drift
  detection.

#### Reverse proxy
- Routing by `Host` + longest matching path prefix, served from an atomically
  swapped routing table (validated snapshots; no request-path locking).
- TLS termination (ACME/Let's Encrypt, manual certs, or none) with TLS 1.2+,
  proactive certificate warmup/renewal, and an ACME host policy tied to routed
  hosts and `networking.baseDomains`.
- HTTP→HTTPS redirect (global or per route) that only trusts
  `X-Forwarded-Proto` from configured trusted proxies.
- Response streaming (`FlushInterval = -1`) for SSE, chunked responses and large
  transfers; WebSocket pass-through; on-the-fly gzip; h2c to the backend;
  per-route read/write/idle timeouts, max body, and custom request/response
  headers.
- Anti-spoofing of `X-Forwarded-For/Proto/Host`, `X-Real-IP` and `Forwarded`
  for untrusted clients; connection reuse via cached transports.
- `/_menina/health` and `/_menina/ready` endpoints.

#### Kubernetes (k3s)
- Managed k3s install (`get.k3s.io`) with Traefik and ServiceLB disabled,
  leaving `:80`/`:443` to Menina's proxy; an existing k3s is left untouched.
- A least-privilege `menina-platform` ClusterRole bound to a `menina`
  ServiceAccount, with the service kubeconfig generated from that identity
  (never admin) and reconciled on every deploy.
- Per-app `Deployment`, `ClusterIP` `Service`, `PersistentVolumeClaim` and
  (`autoscaling/v2`) `HorizontalPodAutoscaler`, with per-environment namespace
  isolation, identity labels, and idempotent applies.
- Reconciliation that applies desired state and prunes removed workloads,
  Services and HPAs; automatic deployment on image push.
- Host proxy → `ClusterIP` routing over kube-proxy DNAT (TCP reachability
  verified by `menina doctor`).

#### Registry
- Bundled Zot registry with a dedicated namespace, PVC, generated config,
  htpasswd auth, and the registry domain routed like any app.
- Push-triggered deploys via Zot's CloudEvents extension (OCI `events` accepted
  too), with repository+tag matching, job deduplication, and superseding.
- Digest pinning (tag → `sha256:…`) for immutable, deterministic deploys and
  rollback.
- Retention (keep the most recent tags; never delete untagged manifests) and
  credential rotation that propagates to Zot and k3s `registries.yaml`.

#### Scaling
- Replica specs accept a fixed count or a `{min, max, targetCPU}` range realized
  as an HPA; a fixed count removes any stale HPA.
- `menina app scale` with a shared **baseline** (all environments) or a
  per-environment **override** (`--environment/-e`, `--baseline`), plus an
  interactive target prompt when the project has multiple environments.

#### CLI
- Project/app/environment management (`menina create`, `project`, `environment`,
  `app create|edit|list|deploy|logs|rollback|scale|delete|restore`).
- Interactive selectors (project → app → action menu) with `+ create a new …`
  first, used by the bare `menina app` / `project` / `environment` commands;
  prompts only on a real TTY (never on pipes/CI/`/dev/null`).
- Guided `menina create` that asks about multiple environments and offers to add
  an app.
- Soft delete by default (workload stopped/scaled to zero, config kept) with
  `restore`, and `--hard` to remove config and prune the workload.
- Observability: `status`, `routes`, `doctor`, `registry status`,
  `registry credential`, `backup`, `notify list|test`.
- Safe `upgrade` (checksum verification, staged execution, atomic replace),
  shell completion `install`, and `--json` on read commands.
- Rich, example-driven help (`Long` + `Examples`) on the key commands.

#### Operations
- Outbound deploy notifications (`webhooks.deploy`) with a stable JSON payload,
  optional HMAC signature, per-project overrides, and best-effort delivery.
- Prometheus metrics on the events listener (`/metrics`).
- Installer script (`install.sh`) that installs and verifies the binary and
  systemd units without provisioning; a Dockerfile and compose stack for
  development.
- Documentation: `docs/CLI.md`, `docs/CONFIGURATION.md`, `docs/PROXY.md`,
  `docs/KUBERNETES.md`, `docs/REGISTRY.md`, `docs/OPERATIONS.md` and the
  top-level README.

### Security
- `serve`/`proxy` run as an unprivileged user under systemd hardening; only the
  proxy is granted `CAP_NET_BIND_SERVICE`.
- Registry webhooks are bearer-token authenticated and fail closed.
- Privileged operations are limited to `install`/`setup`/`deploy`; day-to-day
  commands run as a member of the `menina` group.

### Fixed
- Corrected the `deleted_at` soft-delete convention: the column is now nullable
  and a row is active when `deleted_at IS NULL`.
- Umask-independent permissions on secrets, config and control files.
- `status` degrades gracefully when the control plane is unavailable.

[Unreleased]: https://github.com/moureau-dev/menina/compare/v0.0.1...HEAD
[0.0.1]: https://github.com/moureau-dev/menina/releases/tag/v0.0.1
