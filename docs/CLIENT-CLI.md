# Client CLI (`menina` / `menina-client`)

Menina has two sides:

- **Host CLI** — the Linux binary installed on the VPS. It runs the control
  plane, proxy, registry tooling and the project/app commands.
- **Client CLI** — a cross-platform binary for laptops and CI. It scaffolds and
  validates a repository manifest, applies it to a host over SSH, and builds and
  pushes images.

The host binary also exposes the client commands, so on a VPS you already have
them. On Linux, macOS and Windows, install the standalone client:

```bash
# Linux / macOS
curl -fsSL https://menina.moureau.dev/install-client.sh | sh

# Windows (PowerShell)
irm https://menina.moureau.dev/install-client.ps1 | iex
```

Both installers verify the release checksum and install the command as `menina`.
If a host `menina` is already present, they install `menina-client` instead of
shadowing it. If you download a release binary directly, run `menina install`
(the client TUI offers this on first launch) to copy it to the user bin
directory and update `PATH`. Useful environment variables:

| Variable | Meaning |
| --- | --- |
| `MENINA_CLIENT_VERSION` | Release tag (default: `latest`) |
| `MENINA_CLIENT_DIR` | Install directory |
| `MENINA_CLIENT_BINARY_NAME` | Command name (default: `menina`) |
| `MENINA_CLIENT_LOCAL_BINARY` | Install a locally built binary |
| `MENINA_CLIENT_NO_MODIFY_PATH=1` | Do not edit shell profiles |
| `MENINA_REPO` | GitHub repository |

Run `menina` with no subcommand on a terminal to open the client TUI. It uses
the same menu style as the host, labelled **client** (the host menu is labelled
**host**), and offers Init / Validate / Apply / Build / Push / Version /
Upgrade / Quit. Without a terminal it prints help instead. The TUI re-executes
the binary for the chosen action, so flags and output behave exactly like the
non-interactive commands.

The client needs `curl` (or PowerShell on Windows) for installation, `ssh` for
`apply --host`, and `docker` for `build`/`push`.

## Manifest

A repository manifest is a single `.menina.yml` at the repo root. See
[`examples/.menina.example.yml`](../examples/.menina.example.yml).

```yaml
version: 1
project: demo
environment: production

vars:
  ENVIRONMENT: production
  DOMAIN: example.com

defaults:
  context: .
  tag: ${ENVIRONMENT}
  registry: registry.example.com

storages:
  data:
    size: 1Gi
    path: /data
    # access: single-writer (default) | multi-writer (not supported yet)

apps:
  api:
    dockerfile: apps/api/Dockerfile
    service: { port: 8080 }
    exposure:
      http:
        hosts: ["api.${DOMAIN}"]
    env: { REDIS_URL: "redis://redis:6379" }
    secrets: [DATABASE_URL]
    storage: [data]
    replicas: 1

  game:
    dockerfile: apps/game/Dockerfile
    service: { port: 8080 }
    exposure:
      http:
        hosts: ["game.${DOMAIN}"]
      ports: { tcp: [25565], udp: [19132] }

  redis:
    image: redis:7-alpine
    service: { port: 6379 }

workers:
  jobs:
    from: api
    command: ["/app/worker"]
    replicas: 2
    env: { QUEUE: default }
```

### Top-level fields

| Field | Meaning |
| --- | --- |
| `version` | Schema version; currently `1` (optional, defaults to 1) |
| `project` | Project name in Menina |
| `environment` | Environment to write (default target) |
| `vars` | Non-secret values for `${VAR}` interpolation |
| `source` | `{repo, revision}` ownership hint for the apps declared here; not an authorization boundary |
| `defaults` | `context`, `tag`, `registry` defaults |
| `storages` | Named storage definitions |
| `apps` | Listening workloads (services) |
| `workers` | Non-listening workloads |

`${VAR}` is interpolated from `vars` before parsing. Use the braces form. An
undefined variable is an error; unknown fields are rejected. `vars` are not
passed to containers — set container values under `env`.

### Editor validation

The manifest has a JSON Schema:

```text
https://menina.moureau.dev/schema/v1/menina.schema.json
```

The unversioned `/schema/menina.schema.json` remains an alias. Old clients keep
their embedded schema; new ones should use the versioned URL.

Add this comment at the top of `.menina.yml` for autocomplete and inline
validation in editors that use the YAML language server (VS Code, Neovim,
JetBrains, etc.):

```yaml
# yaml-language-server: $schema=https://menina.moureau.dev/schema/v1/menina.schema.json
```

`menina init` writes that header automatically. The schema covers the manifest
only; the host-side `config.yml`/`project.yml`/`apps/*.yml` files are still
validated by `menina config validate` at apply/deploy time.

### Replicated projects and environments

Because interpolation happens before validation, `project` and `environment`
can themselves be variables:

```yaml
vars:
  PROJECT: demo
  ENVIRONMENT: production

project: ${PROJECT}
environment: ${ENVIRONMENT}
```

Override them at scaffold/apply/build/push time:

```bash
menina apply --project demo-staging --environment staging
menina build --project demo-staging --environment staging --push
menina validate --var PROJECT=demo-staging --var ENVIRONMENT=staging
```

Precedence: `--var KEY=VALUE` (repeatable) first, then `--project` /
`--environment`, which also set the rendered project/environment even when the
manifest uses literal values. Overriding `project` changes the derived image
repository (`<registry>/<project>/<name>`), so build/push target the replicated
project and apply must run for that project too.

### Apps and workers

`apps` are services: they must have `service.port` and get a cluster Service.
Add `exposure.http.hosts` to publish them over HTTP; without that they are
cluster-internal (for example Postgres or Redis). `workers` have no Service,
port or exposure, and no HTTP health probe.

Each workload accepts:

- `dockerfile` (build it) and/or `image` (pull it, or override the repository)
- `context` (build context; defaults to `defaults.context`)
- `service` — `{ port: 8080 }`, the primary container/Service port
- `exposure` — HTTP and raw L4 exposure (see below)
- `command` / `args`
- `env`
- `secrets` — names only, never values
- `buildArgs` — `docker build --build-arg` values (not runtime env, not secrets)
- `storage` — a name, `{ use: name, path: /override }`, an inline
  `{ size, path }`, or a list of those
- `replicas` — a number or `{ min, max, targetCPU }`
- `resources` — `{ cpu, memory }`
- `health` — `{ readiness: /path }` (services only)
- `verification` — declarative checks after rollout (see below)
- `hooks` — opt-in pre-deploy/migrate/post-deploy one-off commands
- `recovery` — explicit failure policy for the app
- `dependsOn` — apps in the same environment that must be ready first

`port` and `host` are shorthands for `service.port` and
`exposure.http.hosts`.

### Exposure

`exposure` splits proxied HTTP traffic from raw L4 ports:

```yaml
apps:
  game:
    dockerfile: apps/game/Dockerfile
    service: { port: 8080 }        # container/Service port
    exposure:
      http:
        hosts: [game.example.com]
        pathPrefix: /
        redirectHttp: true
        headers: { X-Frame-Options: DENY }
        compression: true
        h2c: false                 # gRPC over cleartext HTTP/2
        streaming: true            # SSE/WebSockets
        maxBodyBytes: 0
        timeouts: { read: 30s, write: 60s, idle: 120s }
      ports:
        tcp: [25565]
        udp: [19132]
```

- `exposure.http` goes through the Menina proxy (routing, TLS, headers, proxy
  behavior).
- `exposure.ports` publishes raw ports on the node via `hostPort`, bypassing the
  proxy. Keep L4 apps at one replica.
- `ports` accepts a number, a list (TCP), or `{tcp, udp}` with numbers/lists.
  Port ranges are not supported.
- `exposure.http` requires at least one host. Omit `exposure` for internal-only
  services.
- L4 apps still need `service.port` for the workload's cluster Service.

A worker may use `from: <app>` to reuse the app's image/build, env, secrets and
resources. It does **not** inherit storage or exposure: a worker mounting the
app's SQLite volume would be a second writer, and workers are non-listening. Set
`storage` explicitly on the worker if it needs one.

### Images

- With `dockerfile`, the image is built. The repository defaults to
  `<registry>/<project>/<name>`, where the registry comes from
  `defaults.registry`, `--registry`, or the host's `registry.domain`. Set
  `image` to override the repository. Tag order: `--tag`, `defaults.tag`,
  environment name.
- Without `dockerfile`, `image` is pulled as-is. Pin a tag (`redis:7-alpine`)
  rather than relying on the environment tag.
- Only apps/workers with a `dockerfile` are build targets. External images are
  skipped by `build`/`push`.

### Build args

`buildArgs` are passed to `docker build` as `--build-arg`; they are separate
from the runtime `env` block and from `secrets`:

```yaml
defaults:
  buildArgs:
    NODE_ENV: production

apps:
  web:
    dockerfile: apps/web/Dockerfile
    buildArgs:
      VITE_API_URL: ${PUBLIC_API_URL}
```

```bash
menina build --build-arg VITE_API_URL="$VITE_API_URL" --push
```

Precedence: `defaults.buildArgs`, then the workload's `buildArgs`, then
repeatable `--build-arg KEY=VALUE` flags. Values from `vars` are interpolated.
Build args are **not secrets**: they can appear in image metadata/history. Use
BuildKit secrets for tokens, not build args. A worker using `from` inherits the
app's build args unless it overrides them; building the same image with
different args is rejected.

### Storage

`storages.<name>` has `size`, `path` (container mount point) and `access`:

- `single-writer` (default) forces `replicas: 1`.
- `multi-writer` is rejected: Menina does not configure a ReadWriteMany storage
  class yet.
- One storage per workload; sharing a named storage between workloads is
  rejected. After translation the volume is still one PVC per app.
- Unused storages produce a warning.

### Verification

Verification is separate from reachability. After a rollout, Menina runs the
declared checks and only marks the release `active` when they pass:

```yaml
apps:
  api:
    verification:
      path: /healthz
      method: GET
      expectStatus: [200]
      expectBody: '"status":"ok"'
      expectHeader: { Content-Type: application/json }
      timeout: 10s
      smokeTest:
        command: ["/app/check"]
        timeout: 2m
      failurePolicy: recover      # recover | keep
```

- `path` is checked through the app's public HTTP route and requires HTTP
  exposure.
- `smokeTest` runs as a one-off Job in the app image and exits non-zero on
  failure.
- `failurePolicy: recover` hands the failure to the app's `recovery` policy;
  `keep` leaves the failed release for inspection without restoring.
- A verification failure is deterministic and is not retried.

### Hooks and recovery

Hooks are disabled unless the host sets `security.allowAppHooks: true`. They are
opt-in, timeout-bounded, logged, and run in the app image that is being released:

```yaml
apps:
  api:
    hooks:
      preDeploy:  { command: ["/app/preflight"], timeout: 2m }
      migrate:    { command: ["/app/migrate"],   timeout: 5m }
      postDeploy: { command: ["/app/warmup"],    timeout: 2m }
    recovery:
      policy: require-approval   # previous | none | require-approval
      triggers: [deploy-error, verification-failure, timeout, interrupted]
```

- `previous` restores the previous known-good app image after a qualifying
  terminal failure.
- `none` keeps the failed release for inspection.
- `require-approval` records a `deploy.recovery_pending` event and waits. An
  operator approves it with `menina app rollback <app> --project P --environment E --yes`,
  which restores the previous known-good digest.
- An image rollback never reverses a migration. Apps that declare `migrate`
  default to `require-approval` instead of `none`.
- Recovery is per app; it is not a project-wide transaction.
- After a rollback or recovery, reconciliation pauses for that app so it cannot
  immediately redeploy the failed target. A new push or explicit `app deploy`
  creates a newer release and resumes normal reconciliation.

### Dependency ordering

`dependsOn` is a short, explicit list. Menina waits (bounded) for those apps to
be ready before applying this app and reconciles dependencies first. It does not
build a general orchestration DAG:

```yaml
apps:
  api: { dependsOn: [database] }
workers:
  jobs: { from: api, dependsOn: [api] }
```

### Volume snapshots

Set `snapshotBeforeDeploy: true` on a storage to request a CSI `VolumeSnapshot`
before a release changes the workload. This is opt-in and fails explicitly when
the storage class has no snapshot support. Snapshots are not a substitute for
scheduled backups.

```yaml
storages:
  pgdata:
    size: 10Gi
    path: /var/lib/postgresql/data
    snapshotBeforeDeploy: true
```

### Secrets

`secrets: [DATABASE_URL]` becomes a reference to
`file:<config-dir>/secrets/<project>/<environment>/DATABASE_URL`. Create the
value on the host with:

```bash
# From the client, over SSH (value read from stdin or a hidden prompt)
printf '%s' "$DATABASE_URL" | menina secret set DATABASE_URL \
  --host root@vps --project demo --environment production

menina secret list   --host root@vps --project demo --environment production
menina secret remove DATABASE_URL --host root@vps --project demo --environment production --yes
menina secret rotate DATABASE_URL --host root@vps --project demo --environment production
menina secret rotate API_KEY --host root@vps --project demo --environment production --generate
menina secret rotate API_KEY --host root@vps --project demo --environment production --generate --output ./api-key.txt
menina secret rotate DATABASE_URL --host root@vps --project demo --environment production --restart --yes
menina secret verify DATABASE_URL --host root@vps --project demo --environment production
menina secret verify --host root@vps --project demo --environment production --json
```

The client pipes the value to the host's `menina secret set` over SSH stdin; it
never goes into the manifest, client config, arguments, or logs. `--host` falls
back to `MENINA_HOST` or the cached host used by `apply`. You can also run the
`secret` commands directly on the host.

Secret files are mode `0660`; the environment directory is `0770` with setgid.
Run the command as a user in the `menina` group (or root) so the control plane
can read them. Do not copy files into `secrets/` manually: the control plane may not be
able to read them. `secret verify` checks existence, readability and non-empty
content without printing values. `secret rotate --generate` never prints the
value; use `--output <path>` to write it to a mode-0600 file.

The manifest never contains secret values.

## Client vs host

The standalone client runs the manifest and image commands (`init`, `validate`,
`apply`, `build`, `push`, `schema`, `capabilities`, `install`) plus `secret`,
`job`, `config`, `version`, and `upgrade`. It proxies `apply`, `job`, `config`,
and `secret` over SSH with `--host`.

`app`, `release`, `status`, `routes`, and `doctor` are host commands. Run them
over SSH:

```bash
ssh -t user@vps menina app status api --project demo --environment production
ssh -t user@vps menina release status --project demo --environment production
```

`menina app status` locally fails with `unknown command "app"`.

### Redeploy one app

Config-only changes are applied by the host reconciler. An image change needs a
build and push. To force a release (hooks, verification, release record) for one
app, queue a host deploy.

```bash
# Config-only: env, resources, replicas, exposure, storage
menina validate
menina apply --host user@vps --diff
menina apply --host user@vps --from-plan <plan-id>

# Image change: apply, then rebuild only the changed app
menina validate
menina apply --host user@vps
menina build --push --filter api

# Watch the asynchronous push-triggered deploy
menina job list --host user@vps --json
menina job watch <job-id> --host user@vps --json --timeout 10m

# Force a job/release for one app
ssh -t user@vps menina app deploy api --project demo --environment production --yes
```

`build --push` and `push` accept `--filter`; without it they process every
target. A push does not print the resulting job ID because the host's registry
event listener queues it asynchronously.

## Commands

All client commands are also available on the host binary.

### `init`

```bash
menina init
menina init --project demo --environment production --file .menina.yml
menina init --environment staging --from .menina.yml --file .menina.staging.yml
```

Creates a minimal manifest. `--from` copies a manifest and rewrites its
`environment` and `vars.ENVIRONMENT`, keeping `${ENVIRONMENT}` placeholders.
Refuses to overwrite unless `--force` is passed.

### `install`

```bash
menina install
menina install --dir /usr/local/bin
menina install --no-path
```

Copies the running client to the user bin directory (Windows:
`%LOCALAPPDATA%\Menina\bin`, Unix: `~/.local/bin`, or `/usr/local/bin` as root)
and adds it to `PATH`. Running the client TUI for the first time from an
uninstalled location offers the same step. The release installers do this
automatically; this command covers the direct-download binary.

### `validate`

```bash
menina validate
menina validate -f .menina.yml --registry registry.example.com
menina validate --json
```

Parses, interpolates and renders the manifest. When `config.yml` is available
(via `--config-dir` or the host default), the rendered project is merged with
the existing configuration and validated against it (host conflicts, ports).
Nothing is written.

### `apply`

```bash
menina apply                       # local (host binary)
menina apply --check               # render + validate, write nothing
menina apply --diff                # show file changes, write nothing
menina apply --dry-run --json      # same plan as structured JSON
menina apply --verify              # write, then re-read and verify apps/overrides
menina apply --diff --prune --json # planId + stateToken for review
menina apply --from-plan <plan-id> # execute exactly the reviewed plan
menina apply --if-state-token <t>  # compare-and-swap on current state
menina apply --host deploy@vps     # pipe over SSH
menina apply --host deploy@vps -e staging
```

`apply` is an upsert: it creates the project directory, `project.yml`, missing
environment definition, `apps/<name>.yml`, and per-app overrides. It preserves
an existing `project.yml` and environment definition. It performs no pruning
unless `--prune` is explicitly reviewed and applied via `--from-plan`.

`apply` takes a config-dir lock and records a pre-apply revision under
`<config-dir>/.revisions/`. List them with `menina config revisions` on the host,
or `menina config revisions --host root@vps` from the client; restore with
`menina config rollback <id> --yes` (also available through the client with
`--host`). Revisions restore YAML only, not deployed images. Revisions record
actor, source repo/revision, plan id and a content hash; retention defaults to 50
and is configurable with `MENINA_REVISION_RETENTION`. `.revisions/` is included
in backup/restore. Revisions are not backfilled for applies that happened before
the feature existed.

`--diff`/`--dry-run` save a plan under `<config-dir>/.plans/` and print its
`planId` plus the current `stateToken`. `--from-plan` re-checks the base state
token and every written/deleted file hash before executing, so the reviewed
plan is exactly what runs. `--if-revision` compares the canonical resolved
state token, not the latest pre-apply revision id. `--if-revision` is kept as an
old alias; prefer `--if-state-token`. Plans are kept in `.plans/` (mode 0640,
last 50) and are not included in backups.

`--prune` removes environment overrides for owned apps that this manifest no
longer declares, and removes the app definition when no other environment uses
it. It requires `source.repo` (manifest or `--source-repo`) and refuses to touch
apps owned by another repository. Ownership is also checked on every apply:
overwriting another repository's app is a conflict, and a manifest without
`source.repo` cannot overwrite a remote-owned app.

`--diff`/`--dry-run` print `create`/`update`/`delete` changes and a line diff; with
`--json` the response adds `changes: [{path, action, diff}]` and
`applied: false`. `--verify` re-loads the written project and checks that every
manifest app and environment override exists. `--diff` and `--verify` are
mutually exclusive in effect (verify only runs after a write).

Without `--host`, it applies to the local config directory
(`--config-dir`, default `/etc/menina`) and requires `config.yml`. With
`--host`, it reads the manifest and runs:

```bash
ssh -T <host> menina apply --local -f -
```

The host is cached in the user config directory (`menina/host`, mode 0600).
`MENINA_HOST` is honoured, and `--local` skips the cache. Network auth is left to
`ssh` and your keys/agent; no credentials are stored.

Production confirmation (`security.enforceProdPolicy`) applies to deployments,
not to writing desired state. Build/push and `app deploy` remain the rollout
steps.

### `build`

```bash
menina build                       # build every manifest image
menina build --push                # build then push
menina build --push --filter api,web
menina build --filter 'api*' --tag staging
menina build --dry-run
```

Builds each `dockerfile` target with its manifest repository/tag and context
using the local Docker daemon. `--filter` takes comma-separated names or globs
and selects build targets only. `--push` pushes each image after building.
`--build-arg KEY=VALUE` overrides manifest build args and is repeatable.

### `push`

```bash
menina push                        # push prebuilt images
menina push --build                # build first
menina push --build --filter api
```

Pushes the manifest's built images. External `image:` apps are skipped. A push
triggers auto-deploy for enabled apps that were already applied.

### `job`

`job watch <id> [--cursor N]` resumes after the last event ID, for example after
an SSH drop. The final event carries `terminal: true` and the remote exit code.

```bash
menina job list --host root@vps
menina job watch <id> --host root@vps --json --timeout 10m
```

The client proxies `ssh -T <host> menina job ...`; stdout/stderr and the remote
exit code are propagated. `--json` is NDJSON for `job watch`.

### `schema` and `capabilities`

```bash
menina schema                  # embedded manifest JSON Schema
menina schema --json           # {schemaVersion, schemaUrl, schema}
menina schema --version
menina schema --url
menina capabilities --json     # version, os/arch, schema version, feature flags, commands
menina capabilities --host root@vps --json
```

`capabilities` is local by default. With `--host` it runs
`ssh <host> menina capabilities --json`, includes the host document and a
`compatibility` object (`schemaMatch`, `versionMatch`, `compatible`, `warnings`).
A schema mismatch is a hard incompatibility; a plain version difference is a
warning. This is the intended preflight for agents and CI.

### Common flags

| Flag | Commands | Meaning |
| --- | --- | --- |
| `-f`, `--file` | all | Manifest path; `-` reads stdin |
| `--project` | all | Override the manifest project |
| `--var` | all | Override a manifest variable (`KEY=VALUE`, repeatable) |
| `-e`, `--environment` | all | Override manifest environment (also rewrites `${ENVIRONMENT}`) |
| `--registry` | validate/apply/build/push | Registry host for built repositories |
| `--tag` | all | Override the image tag |
| `--build-arg` | build/push | `docker build --build-arg KEY=VALUE` (repeatable) |
| `--json` | all | Machine-readable output |
| `--non-interactive` | all | Never prompt or open the TUI; fail instead |
| `--config-dir` | all | Host config directory used for secret refs/validation |

### `version` and `upgrade`

```bash
menina version                 # installed version
menina version --check         # compare with the latest GitHub release
menina upgrade --check         # same, upgrade-oriented output
menina upgrade                 # download, verify, replace the current binary
menina upgrade --version v0.2.0
menina upgrade --force         # reinstall even when already current
```

`upgrade` selects the `menina-client-<os>-<arch>` asset for the running
platform, verifies it against `checksums.txt`, executes it as a sanity check,
then replaces the running executable. On Unix the replacement is an atomic
rename; on Windows the previous executable is renamed `.old` and removed on the
next upgrade. The install directory must be writable (re-run with `sudo` if it
is a system directory). `MENINA_REPO` overrides the GitHub repository.

`version --check` and `upgrade` are standalone-client commands. The host binary
has its own `version` and Linux-host `upgrade` commands.


## Deploy flow

```bash
# 1. Write desired state (creates/updates project, apps, overrides)
menina apply --host deploy@vps

# 2. Build and push (push triggers the deploy for built apps)
menina build --push

# Config-only changes (env, replicas, host, storage)
menina apply --host deploy@vps
```

External images (`redis`, `postgres`) have no push event; they are deployed
from the applied desired state by reconciliation (or explicitly with
`menina app deploy`). Built images wait for their push.

Failure order is deliberate: if `apply` fails, nothing is built; if a push
fails, the previous digest keeps running.

## CI

Build/push needs no SSH, no root and no host binary — only the registry
credential:

```bash
echo "$REGISTRY_PASSWORD" | docker login "$REGISTRY_HOST" -u "$REGISTRY_USER" --password-stdin
docker build -t "$REGISTRY_HOST/demo/api:production" apps/api
docker push  "$REGISTRY_HOST/demo/api:production"
```

Or use the client (same result, manifest-driven):

```bash
menina build -f .menina.yml --push --filter api
```

Applying config from CI requires SSH and should run only on a protected branch,
ideally with a restricted key:

```text
command="menina apply --local -f -",no-pty,no-port-forwarding ssh-ed25519 ...
```

```yaml
- name: apply desired state
  if: github.ref == 'refs/heads/main'
  run: ssh -T deploy@host menina apply --local -f - < .menina.yml
- name: build and push
  run: menina build -f .menina.yml --push
```

`--filter` is the explicit selector for CI agents; there is no `--changed` or
git-diff mode. Compute a changed list in your CI and pass it to `--filter`.

## Multiple manifests

Multiple manifests may contribute to the same project/environment, for example
one per repository. Each writes its own `apps/<name>.yml` and
`environments/<env>/<name>.yml`; existing `project.yml` and environment
definitions are kept.

Constraints:

- Workload names must be unique per project/environment. Ownership is enforced
  when `source.repo` is declared: another repository cannot overwrite an owned
  app. A manifest without `source.repo` also cannot overwrite a remote-owned app.
- Named storages are manifest-local; two manifests using the same storage name
  do not share a volume.
- Removing a workload from a manifest does not delete it. Use
  `menina app delete`.
- `apply` validates against the whole host configuration, so duplicate hosts or
  ports are rejected even across manifests.

## Limitations

- No git-diff selection (`--filter` is explicit) and no multi-node/HA support.
  Recovery is an explicit host-side per-app policy, not a client default. Prune
  requires a reviewed `--from-plan`; ownership is a hint, not authorization.
- One storage per workload; named volumes cannot be shared across workloads;
  multi-writer/RWX is not configured by Menina.
- No compose semantics: `dependsOn` is only an explicit readiness ordering, not
  automatic provisioning or networking. Services reach each other by Kubernetes
  Service name.
- No build on the host: `apply` is config-only; `build`/`push` use the local
  Docker daemon (or plain CI).
