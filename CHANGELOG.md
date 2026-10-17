# Changelog

All notable changes to Menina are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.0.10] - 2026-10-17

### Added
- Repository manifests (`.menina.yml`) and a cross-platform client:
  `menina init`, `validate`, `apply`, `build`, and `push`. Multiple manifests can
  contribute apps/workers to the same project/environment; apply is an upsert and
  never prunes.
- `menina init` scaffolds a manifest and can copy one with `--from`, rewriting
  the environment and `vars.ENVIRONMENT`.
- `project` and `environment` may be `${VAR}` values. All client commands accept
  `--project`, `-e/--environment`, and repeatable `--var KEY=VALUE` overrides,
  applied before interpolation, so the same manifest can target replicated
  projects/environments (including derived image repositories).
- `menina validate` parses, interpolates and renders a manifest; when
  `config.yml` is available it also resolves the rendered project against the
  host configuration.
- `menina apply` writes project/app/environment YAML locally or pipes it over
  SSH (`--host`, cached under the user config directory); `--check` validates
  without writing.
- `menina build` / `menina push` build and push manifest images with the local
  Docker daemon, including `--filter` name/glob selection. Push triggers the
  normal deploy for already-applied apps.
- Standalone `menina-client` binary for Linux, macOS and Windows
  (`cmd/menina-client`); the host binary also exposes the client commands.
- Installers (`scripts/install-client.sh`, `scripts/install-client.ps1`) that
  verify checksums, install the command as `menina` (or `menina-client` when a
  host `menina` exists) and update `PATH`. Release workflow publishes client
  assets for linux/darwin/windows on amd64/arm64.
- `menina version --check` and `menina upgrade` for checksum-verified client
  self-updates.
- A client TUI (bare `menina` on a terminal) with Init/Validate/Apply/Build/
  Push/Version/Upgrade. Both root menus now carry a small badge: `HOST` or
  `CLIENT`.

### Changed
- Manifest storage is intentionally conservative: one storage per workload,
  named volumes cannot be shared, `single-writer` forces one replica, and
  `multi-writer` is rejected until Menina configures an RWX storage class.
- Worker `from: <app>` inherits image/build/env/secrets/resources but never
  storage, preventing accidental second writers to SQLite.

### Fixed
- Manifest secret references use forward-slash host paths, so rendering on
  Windows/macOS produces references a Linux host can resolve.
- Example manifest updated to validate: single-writer storage, matching SQLite
  mount path, a separate worker volume, and no multi-writer/replicas conflict.

### Docs
- `docs/CLIENT-CLI.md`: manifest schema, commands and flags, SSH host caching,
  deploy ordering, CI patterns, multi-manifest rules and limitations.
- `llms.txt`, `README.md` and `docs/README.md` describe the client, installers,
  TUI and client commands.
- Website doc sync now rewrites relative Markdown links to absolute
  `https://menina.moureau.dev/docs/...` URLs.

## [0.0.9] - 2026-10-16

### Added
- `menina app deploy`/`rollback` accept `--no-wait` (queue and return) and
  `--timeout`; the success message now includes the job id and the config
  snapshot version used.
- `menina app status` — last deployment job, release digest and observed phase.
- `menina job list` / `menina job cancel <id>` — inspect and cancel deployment
  jobs (control-plane `/jobs` and `/jobs/cancel` endpoints).
- `menina project get <name>` — show a project's namespace, apps and environments.
- `menina config validate` — resolve and validate the whole config tree without
  applying; exits non-zero when invalid.
- `menina app reset-volume` — scale to zero, delete the app's PVC and redeploy so
  the volume is recreated empty.

### Fixed
- Apps pointing at an external registry (e.g. `redis:alpine`) no longer fail
  digest resolution against the bundled Zot: digests are resolved only for
  bundled-registry images, and other images deploy by tag.

## [0.0.8] - 2026-10-15

### Added
- Per-app environment overrides: `environments/<env>/<app>.yml` (one file per
  app) so several repositories can contribute apps to the same project and
  environment without ever writing the same file. Directory overrides win over
  an inline `apps:` map, and `menina app scale -e <env>` writes a single per-app
  file instead of rewriting the whole environment file.
- `source: {repo, revision}` on an app records the repository that owns it;
  `SourceSpec.ConflictsWith` supports ownership collision checks (blank/omitted
  means locally owned).
- App secrets: `secrets: {ENV_VAR: ref}` on an app or environment override,
  resolved at deploy time into a Kubernetes Secret named `<app>-secrets` and
  injected as environment variables via `valueFrom.secretKeyRef`. Only the
  reference is stored in configuration; values never enter the config tree, the
  resolved snapshot, or `--json` output.
- `menina secret set|list|remove` stores app secret values on the host
  (`secrets/<project>/<environment>/<name>`, group-writable, outside the config
  tree). Values are read from stdin — hidden on a terminal — or `--file`, never
  from arguments, and are never printed.

## [0.0.7] - 2026-10-15

### Added
- Interactive terminal UI (Bubble Tea). Bare `menina` opens a full-screen menu
  (context-aware: only Setup/Deploy before the platform is ready, hidden behind
  `--show-all` afterwards), and every prompt — text, select and confirm — plus a
  file picker for `restore` are TUI widgets. Output from menu actions is shown in
  a titled box above the menu. Arrow keys scroll one row at a time, `/` filters,
  `Shift+Tab`/`Esc` goes back (menus step up; inline flows return to their list)
  and `Ctrl+C` must be pressed twice to quit. Non-TTY keeps the line-based
  prompter.
- App repository now defaults to the bundled registry
  (`registry.<domain>/<project>/<app>`) when the registry is enabled, and `y`/`n`
  are accepted as aliases on Yes/No select menus.

### Fixed
- `menina registry credential --username` / `--password` now print just that
  field. They were previously an input flag for regeneration, so running
  `--password` on its own errored with "flag needs an argument"; the explicit
  regeneration inputs are now `--new-username` / `--new-password`.

### Docs
- `llms.txt`: SSH and CI automation (TTY vs flags, `--json`, a CI build-and-push
  recipe), the registry push→deploy mapping and the single shared push/pull
  credential.

## [0.0.6] - 2026-10-14

### Added
- L4 exposure for non-HTTP protocols: `exposure.ports` (TCP/UDP) binds raw ports
  on the node (`hostPort`) so game servers, TURN/STUN for WebRTC, and other
  non-HTTP services can run on Menina without going through the HTTP proxy.
  Accepts a number, a list, or `{tcp, udp}` (each number or list); `menina
  deploy` opens the ports with ufw when present; `menina routes` lists them.

## [0.0.5] - 2026-10-14

### Added
- `menina backup` now produces a portable `tar.gz` bundle (SQLite `VACUUM INTO`
  snapshot, `config.yml` + `projects/`, TLS material, `manifest.json`) with
  opt-in `--with-secrets` (optionally `--passphrase`-encrypted), `--with-volumes`
  (app PVC data) and `--with-images` (registry blobs), captured directly from the
  k3s `local-path` storage (crash-consistent by default; `--consistent` quiesces
  the workload).
- `menina restore <archive>` applies a bundle (DB, config, secrets, certs,
  volumes/images), with `--force` to overwrite.

## [0.0.4] - 2026-10-14

### Fixed
- `menina upgrade`: the downloaded binary was staged without the executable bit
  (and under `/tmp`), so the pre-install verification failed with
  `...: permission denied`. It is now staged in the install directory with mode
  `0755` (also avoiding `noexec /tmp`).

### Docs
- `docs/PROXY.md`: document Cloudflare **Full (strict) with origin TLS (ACME)**
  as the alternative to per-hostname Flexible rules, including the
  `httpRedirect: false` (no forced redirect/HSTS) option and grey-clouding the
  registry.

## [0.0.3] - 2026-10-14

A setup UX polish release.

### Changed
- `menina setup` now uses numbered select menus with a one-line description per
  option instead of `[y/N]` prompts (k3s/Traefik, install Zot, networking mode,
  Cloudflare DNS automation and orange cloud, TLS termination and certificate
  strategy, registry credential, deploy webhook, HMAC signing, and review). The
  "Do you account for multiple environments?" prompt uses the same style.
  Destructive confirmations (delete/abort) remain `[y/N]`.

## [0.0.2] - 2026-10-14

A bug-fix release focused on the interactive CLI and release builds, plus
clearer Cloudflare guidance for running behind a proxy without giving Menina an
API token.

### Fixed
- Release builds now record the Go toolchain, so `menina version` reports it
  instead of `unknown`.
- The interactive CLI can now be exited: selectors and the menus offer an
  explicit `Exit`/`Back`, and Ctrl+C works mid-prompt. Previously
  `signal.NotifyContext` consumed SIGINT while a prompt blocked on `ReadString`,
  so interactive commands could not be interrupted.

### Changed
- `menina setup`: the Cloudflare DNS prompt now states that an API token is only
  needed for automation ("n" = you manage DNS yourself), avoiding a confusing
  token request for manual/orange-cloud setups.

### Documentation
- `docs/PROXY.md`: new "Cloudflare in front, Menina serves HTTP only (no token
  needed)" section — minimal config for orange-cloud + HTTP origin, the Flexible
  SSL caveat, `trustedProxies`, and the warning to keep the registry DNS-only
  (Cloudflare's proxy caps uploads/timeouts and breaks `docker push`).

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

[Unreleased]: https://github.com/moureau-dev/menina/compare/v0.0.6...HEAD
[0.0.6]: https://github.com/moureau-dev/menina/releases/tag/v0.0.6
[0.0.5]: https://github.com/moureau-dev/menina/releases/tag/v0.0.5
[0.0.4]: https://github.com/moureau-dev/menina/releases/tag/v0.0.4
[0.0.3]: https://github.com/moureau-dev/menina/releases/tag/v0.0.3
[0.0.2]: https://github.com/moureau-dev/menina/releases/tag/v0.0.2
[0.0.1]: https://github.com/moureau-dev/menina/releases/tag/v0.0.1
