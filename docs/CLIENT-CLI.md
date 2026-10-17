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
shadowing it. Useful environment variables:

| Variable | Meaning |
| --- | --- |
| `MENINA_CLIENT_VERSION` | Release tag (default: `latest`) |
| `MENINA_CLIENT_DIR` | Install directory |
| `MENINA_CLIENT_BINARY_NAME` | Command name (default: `menina`) |
| `MENINA_CLIENT_LOCAL_BINARY` | Install a locally built binary |
| `MENINA_CLIENT_NO_MODIFY_PATH=1` | Do not edit shell profiles |
| `MENINA_REPO` | GitHub repository |

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
    port: 8080
    host: api.${DOMAIN}
    env: { REDIS_URL: "redis://redis:6379" }
    secrets: [DATABASE_URL]
    storage: [data]
    replicas: 1

  redis:
    image: redis:7-alpine
    port: 6379

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
| `defaults` | `context`, `tag`, `registry` defaults |
| `storages` | Named storage definitions |
| `apps` | Listening workloads (services) |
| `workers` | Non-listening workloads |

`${VAR}` is interpolated from `vars` before parsing. Use the braces form. An
undefined variable is an error; unknown fields are rejected. `vars` are not
passed to containers — set container values under `env`.

### Apps and workers

`apps` are services: they must have a `port` and get a cluster Service. Add
`host` to publish them; without `host` they are internal-only (for example
Postgres or Redis). `workers` have no port, no Service, no `host`, and no HTTP
health probe.

Each workload accepts:

- `dockerfile` (build it) and/or `image` (pull it, or override the repository)
- `context` (build context; defaults to `defaults.context`)
- `command` / `args`
- `env`
- `secrets` — names only, never values
- `storage` — a name, `{ use: name, path: /override }`, an inline
  `{ size, path }`, or a list of those
- `replicas` — a number or `{ min, max, targetCPU }`
- `resources` — `{ cpu, memory }`
- `health` — `{ readiness: /path }` (services only)

A worker may use `from: <app>` to reuse the app's image/build, env, secrets and
resources. It does **not** inherit storage: a worker mounting the app's SQLite
volume would be a second writer. Set `storage` explicitly on the worker if it
needs one.

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

### Storage

`storages.<name>` has `size`, `path` (container mount point) and `access`:

- `single-writer` (default) forces `replicas: 1`.
- `multi-writer` is rejected: Menina does not configure a ReadWriteMany storage
  class yet.
- One storage per workload; sharing a named storage between workloads is
  rejected. After translation the volume is still one PVC per app.
- Unused storages produce a warning.

### Secrets

`secrets: [DATABASE_URL]` becomes a reference to
`file:<config-dir>/secrets/<project>/<environment>/DATABASE_URL`. Create the
value on the host with:

```bash
printf '%s' "$DATABASE_URL" | menina secret set DATABASE_URL \
  --project demo --environment production
```

The manifest never contains secret values.

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
menina apply --host deploy@vps     # pipe over SSH
menina apply --host deploy@vps -e staging
```

`apply` is an upsert: it creates the project directory, `project.yml`, missing
environment definition, `apps/<name>.yml`, and per-app overrides. It preserves
an existing `project.yml` and environment definition. It performs no pruning.

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

### `push`

```bash
menina push                        # push prebuilt images
menina push --build                # build first
menina push --build --filter api
```

Pushes the manifest's built images. External `image:` apps are skipped. A push
triggers auto-deploy for enabled apps that were already applied.

### Common flags

| Flag | Commands | Meaning |
| --- | --- | --- |
| `-f`, `--file` | all | Manifest path; `-` reads stdin |
| `-e`, `--environment` | all | Override manifest environment (also rewrites `${ENVIRONMENT}`) |
| `--registry` | validate/apply/build/push | Registry host for built repositories |
| `--tag` | all | Override the image tag |
| `--json` | all | Machine-readable output |
| `--config-dir` | all | Host config directory used for secret refs/validation |

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

- Workload names must be unique per project/environment. A later apply with the
  same name overwrites the earlier definition; there is no `source.repo`
  ownership tracking in manifests yet.
- Named storages are manifest-local; two manifests using the same storage name
  do not share a volume.
- Removing a workload from a manifest does not delete it. Use
  `menina app delete`.
- `apply` validates against the whole host configuration, so duplicate hosts or
  ports are rejected even across manifests.

## Limitations

- No `--prune`, no manifest ownership/conflict detection, no git-diff selection.
- One storage per workload; named volumes cannot be shared across workloads;
  multi-writer/RWX is not configured by Menina.
- No compose semantics: no `depends_on`, no automatic database provisioning, no
  networks. Services reach each other by Kubernetes Service name.
- No build on the host: `apply` is config-only; `build`/`push` use the local
  Docker daemon (or plain CI).
