# Changelog

All notable changes to Menina are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.3] - 2026-10-23

### Added
- Operational state contract with shared contract tests, an in-memory fake for
  controller tests, and an existing-database migration test.
- Versioned API command dispatcher with actor context, in-memory idempotency,
  shared transport parity tests, and a stdio adapter.
- API-first desired-state mutations. `apply`, `config revisions`, and
  `config rollback` execute through the API with server-side base-state and
  file-hash revalidation, falling back to local files when the control plane is
  down.
- Identity profile model and storage, with scopes, attached keys, expiry,
  status, last-seen tracking, and server-side key resolution. Adds migration
  `0006_identity`.
- One-time enrollment tokens with short TTL, hashed-at-rest storage, and
  transactional consumption that attaches a key to a profile. Adds migration
  `0007_enrollment_tokens`.
- Key lifecycle operations: list keys, parse authorized-key fingerprints,
  revoke one key without disturbing others, rotate a key in one transaction,
  and set or clear key expiry.
- Key-derived actors and SSH forced commands. Authenticated keys resolve to
  profiles, allowlisted commands run with a server-injected actor, the API
  bridge accepts that actor over the local socket, and `menina identity`
  manages profiles, keys, and enrollment tokens.
- Scope enforcement for API commands and forced commands, with global,
  project-scoped, and environment-scoped grants.
- Audit capture and query for API and forced-command actions, including actor,
  key fingerprint, target, project/environment/app, outcome, and timestamps.
  Adds migration `0008_audit`.
- Authorization policy with global, project, environment, and app-level scopes,
  enforced by the API authorizer and forced-command wrapper.
- Server-side ownership enforcement. `source.repo` is checked for apply and
  prune plans, so a client cannot overwrite or prune another repository's apps.
- Client contexts with `--profile` / `MENINA_PROFILE`, SSH host-key pinning,
  and `whoami` for the server-derived profile.

## [0.1.2] - 2026-10-23

### Added
- Optional `menina-core` and `menina-edge` binaries for running the controller
  and edge proxy as separate roles.
- HTTP and SSH API transport adapters. HTTP is disabled by default and can be
  enabled with `serve --api-listen`; it uses the control token and should bind
  to loopback until identity and scopes land.

### Changed
- Reorganized packages under `api/`, `cli/`, `contract/`, `core/`, `edge/`,
  `provision/`, and `telemetry/`.
- Split the control plane into controller, worker, and API transport packages.
  The CLI continues to use the Unix-socket API for running-state commands.
- Moved migrations to `core/state/sqlite/migrations` without changing their
  filenames or recorded schema versions.

### Docs
- Updated `docs/STRUCTURE.md`, package references, examples, and the roadmap for
  the new layout and remaining API, state, and monitoring work.

## [0.1.1] - 2026-10-18

### Fixed
- Kubernetes probes now include a startup grace period and realistic timeouts
  (readiness 5s delay/5s timeout/5s period, liveness 30s/5s/10s), preventing
  kubelet from restarting healthy apps during startup or short control-plane
  stalls.
- Legacy and terminal job records report `ExitCode: 0` for `done` and `1` for
  `failed`, and fall back to `UpdatedAt` when no finish time was recorded.

### Changed
- `app status` now returns a concrete usage hint when the app name is missing.

### Docs
- Document the client/host command split, SSH-only `app`/`release` commands,
  single-app redeploy recipes, `build`/`push --filter`, and the schema-shaped
  manifest example.

## [0.1.0] - 2026-10-18

### Added
- Per-app releases with explicit states and a durable state machine
  (`prepared`, `migrating`, `deploying`, `verifying`, `active`, `failed`,
  `recovering`, `rolled_back`), pinned image/config snapshots, previous-good
  pointer, verification/migration status, and release events. `release status`
  aggregates an environment without hiding failed components;
  `release history` shows an app's records.
- Declarative verification: expected HTTP status/body/header and an optional
  smoke-test Job, with `failurePolicy: recover|keep`. `deploy --verify` remains a
  reachability check.
- Opt-in lifecycle hooks (`preDeploy`, `migrate`, `postDeploy`) with timeouts,
  logs, image pinning and `security.allowAppHooks`. A one-off Kubernetes Job
  primitive backs hooks and smoke tests.
- Explicit per-app recovery (`previous` / `none` / `require-approval`) with
  independent triggers. Migrations default to `require-approval`; recovery never
  claims to reverse data.
- First-class promotion by existing digest with a reviewed target plan and
  state token (`app promote --dry-run/--state-token`).
- Minimal dependency ordering via `dependsOn`; no general orchestration DAG.
- Optional CSI volume snapshots before deploy (`snapshotBeforeDeploy`), failing
  explicitly when the storage class does not support them.
- Job watch resume cursor (`--cursor`) and a terminal event carrying the remote
  exit code; clean cancellation remains.
- Webhook delivery history with idempotency keys, dead-letter visibility and
  replay (`notify deliveries`, `notify replay`).
- Metrics for releases, deploy duration/outcome, queue depth, app replicas,
  proxy readiness and node/disk capacity; reference alert rules in
  `docs/OPERATIONS.md`.
- Daily scheduled backup service/timer installed by `menina deploy platform`,
  plus RPO/RTO and restore-drill guidance.

### Fixed
- Namespaced external images such as `traefik/whoami` are no longer treated as
  bundled-registry repositories and no longer fail digest resolution; only the
  configured registry host is resolved to a digest.
- The proxy unit no longer owns the shared `RuntimeDirectory`, so restarting it
  cannot unlink the control socket/token created by `menina-serve`.
- The control-plane service identity can create/delete batch Jobs (hooks,
  verification smoke tests) and CSI VolumeSnapshots.
- Deployment applies retry Kubernetes update conflicts instead of failing a
  release when reconciliation writes concurrently.
- No-op deploys link the job to the existing active release, and releases left
  non-terminal by a crashed control plane are marked failed on restart.
- Hard-deleted apps drop their observed row so metrics and status do not keep a
  ghost component.
- `menina deploy platform` also refreshes the scoped service RBAC/kubeconfig, so
  new Job and VolumeSnapshot permissions reach existing hosts.

### Changed
- Deployment jobs pin the resolved config snapshot/version at queue time; a job
  no longer executes against configuration written after it was queued.
- Deployments are annotated with a hash of injected secrets; `app status`
  reports `secret rotated, rollout stale` and a rotation forces a rollout when
  `--restart` queues a deploy.
- Control-plane HTTP failures map to stable CLI error classes (auth, not found,
  conflict, validation, unsupported, timeout).

### Docs

- Added `AGENTS.md` and `docs/STRUCTURE.md` covering release/commit rules, doc
  maintenance, package boundaries, and when to update the structure guide.
- Added an implementation dependency map to `ROADMAP.md` with per-item
  prerequisites, difficulty/importance, and the hidden one-off job prerequisite.
- Clarified the current service-to-service/microservices boundary in `llms.txt`
  and removed the stale Tier 4 placement of migration jobs/dependency ordering
  from `ROADMAP.md`.
- Expanded `ROADMAP.md` with a per-app-first release model (project releases as
  partial-failure aggregates), explicit recovery policies (`previous` / `none` /
  `require-approval`), declarative verification, promotion by digest + reviewed
  plan, migration hooks/snapshots, fleet operations, and a shared-access security
  gate.
- Rewrote `llms.txt` role-first: choose client vs host, client manifest/apply
  safety contract, shared deployment model, then host platform details. Detailed
  reference remains in the subdocs.

## [0.0.14] - 2026-10-17

### Added
- Client `config revisions` / `config rollback --host` proxy the host commands
  over SSH; `config revisions --json` returns `[]` instead of `null` when none
  exist.

### Docs
- Corrected stale manifest docs (prune, ownership, multiple-manifest and
  limitation wording), documented `secret rotate --output` and client config
  passthrough, and noted that revisions are not backfilled.
- Added `ROADMAP.md`, explicitly decomposing multi-node/HA into control-plane
  state, k3s provisioning, replicated storage, proxy/routing, registry, and DR.

## [0.0.13] - 2026-10-17

### Added
- Apply concurrency: canonical resolved `stateToken`, config-dir lease lock with
  heartbeat/stale recovery, and `apply --if-revision <token>` (compare-and-swap).
  `apply --diff` saves a plan under `.plans/` and prints `planId`; `apply
  --from-plan <id>` re-checks the base state token and every file hash before
  executing.
- Schema versioning: `https://menina.moureau.dev/schema/v1/menina.schema.json`
  (unversioned alias kept), `menina schema --version`, and a reflection-based
  parser/schema contract test.
- Revision metadata: actor, source repo/revision, plan id, content hash; `.revisions/`
  is included in backup/restore; retention is configurable with
  `MENINA_REVISION_RETENTION` (default 50).
- `secret list --references` shows referencing apps; `secret rotate --restart`
  queues deployments for those apps (explicit, production needs `--yes`).
- Client `job list --host` and `job watch --host` proxy `ssh -T` NDJSON with
  timeout and remote exit-code propagation.

- Manifest `source: {repo, revision}` ownership. Apply refuses to overwrite an
  app owned by a different repository (and a manifest without `source.repo`
  cannot overwrite a remote-owned app). `apply --prune` removes overrides for
  owned apps no longer declared, and removes the app definition when no other
  environment uses it.
- `apply` records pre-apply revisions under `<config-dir>/.revisions/`;
  `menina config revisions` lists them and `menina config rollback <id> --yes`
  restores the affected YAML files.
- `menina job watch <id>` streams NDJSON job snapshots until done/failed/cancelled
  with `--json` and `--timeout`. The control plane exposes `/jobs/watch`; the
  streaming client no longer inherits the 10-second request timeout.

- `menina app deploy --verify` checks the app's public HTTP route(s) after the
  rollout and TCP-dials configured L4 ports. `--verify` cannot be combined with
  `--no-wait`.
- `menina app logs --since D` bounds logs by age (Kubernetes `sinceSeconds`), in
  addition to `--tail`; the deployment log path previously ignored `Since`.
- `menina secret rotate <name>` replaces a value from stdin/`--file`, or
  generates one with `--generate`; the client proxies it over SSH.

- `menina capabilities --host user@vps --json` queries the host over SSH and
  reports schema/version compatibility (schema mismatch is incompatible; a
  version difference is a warning).
- `apply --diff` / `--dry-run` shows create/update file plans and line diffs
  without writing; `--json` adds `changes: [{path, action, diff}]`. `apply
  --verify` re-reads the written project and confirms apps and overrides exist.
- Classified CLI errors: with `--json`, failures use
  `{"error":{"code","message","hint"}}`; exit codes are stable
  (1 generic, 2 validation, 3 not found, 4 auth, 5 timeout, 6 conflict,
  7 unsupported). Validation and deployment-timeout paths are classified.

- `menina schema [--json] [--url]` prints the embedded `.menina.yml` JSON
  Schema; `menina capabilities --json` reports version, OS/arch, schema version,
  feature flags and commands for agents/CI.
- Global `--non-interactive` on the host and client: never prompt or open the
  TUI; fail with a clear error instead.
- `menina secret verify [name]` checks that stored secret files exist, are
  readable and are not empty, without printing values. The client proxies it
  over SSH.
- Environment hard delete now removes `environments/<env>/` per-app overrides;
  soft delete hides them behind the `.trash` tombstone so the environment no
  longer appears until restored.
- Deployment timeouts now name the job and suggest `--timeout`/`--no-wait`.

- Manifest `service: { port }` and an `exposure` block: `exposure.http`
  (`hosts`, `pathPrefix`, `redirectHttp`, `headers`, `compression`, `h2c`,
  `streaming`, `maxBodyBytes`, `timeouts`) and `exposure.ports` for raw
  TCP/UDP (`hostPort`). `port`/`host` remain available as shorthands.

### Docs
- Website copy: clarify that digest pinning/immutable releases apply to
  bundled-registry images (external images deploy by tag), mention repository
  manifests in the app-definition step, and describe the installer's full scope.
- Add a `.menina.yml` JSON Schema at
  `https://menina.moureau.dev/schema/v1/menina.schema.json` (unversioned alias
  kept), serve it from the
  website, and add the `yaml-language-server` header to `menina init` and the
  example manifest.
- Quickstart now follows the manifest flow (`init` → `apply` → `build --push`)
  instead of skipping the app definition.
- Manifest JSON Schema now documents `service`, `exposure.http`, and
  `exposure.ports`; the example and `init` template use the nested form.

## [0.0.12] - 2026-10-17

### Fixed
- Digest resolution now treats bare image names (`redis:7-alpine`) as external
  images instead of assuming they belong to the bundled registry. Previously
  Zot returned 404 for `/v2/redis/manifests/7-alpine`, leaving the deploy job
  pending; they now deploy by tag as documented.

## [0.0.11] - 2026-10-17

### Added
- `menina install` copies a directly downloaded client binary into the user bin
  directory and updates `PATH` (Windows user PATH via PowerShell). The client
  TUI offers the same install step on first launch from an uninstalled location.
- Client `secret set|list|remove` commands for standalone clients: they proxy
  the host's `menina secret` command over SSH, passing values on stdin (hidden
  prompt on a TTY), never through the manifest or arguments.
- Manifest `buildArgs` (`defaults.buildArgs` + per-workload) and repeatable
  `--build-arg KEY=VALUE` on `build`/`push`, passed to `docker build` with
  overrides. Worker `from` inheritance includes build args; building the same
  image with conflicting args is rejected.

### Fixed
- The TUI `host`/`client` badge is now small muted lowercase text instead of a
  padded, filled block.

### Docs
- Website: label the server install command and add Linux/macOS/Windows client
  buttons linking directly to the latest release assets. The client links are
  upgraded to arm64 in `hydrate` when the browser exposes an ARM architecture;
  the Docs link moved to the navbar and the redundant hero links were removed.

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

[Unreleased]: https://github.com/moureau-dev/menina/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/moureau-dev/menina/releases/tag/v0.1.1
[0.1.0]: https://github.com/moureau-dev/menina/releases/tag/v0.1.0
[0.0.14]: https://github.com/moureau-dev/menina/releases/tag/v0.0.14
[0.0.13]: https://github.com/moureau-dev/menina/releases/tag/v0.0.13
[0.0.12]: https://github.com/moureau-dev/menina/releases/tag/v0.0.12
[0.0.11]: https://github.com/moureau-dev/menina/releases/tag/v0.0.11
[0.0.10]: https://github.com/moureau-dev/menina/releases/tag/v0.0.10
[0.0.9]: https://github.com/moureau-dev/menina/releases/tag/v0.0.9
[0.0.8]: https://github.com/moureau-dev/menina/releases/tag/v0.0.8
[0.0.7]: https://github.com/moureau-dev/menina/releases/tag/v0.0.7
[0.0.6]: https://github.com/moureau-dev/menina/releases/tag/v0.0.6
[0.0.5]: https://github.com/moureau-dev/menina/releases/tag/v0.0.5
[0.0.4]: https://github.com/moureau-dev/menina/releases/tag/v0.0.4
[0.0.3]: https://github.com/moureau-dev/menina/releases/tag/v0.0.3
[0.0.2]: https://github.com/moureau-dev/menina/releases/tag/v0.0.2
[0.0.1]: https://github.com/moureau-dev/menina/releases/tag/v0.0.1
