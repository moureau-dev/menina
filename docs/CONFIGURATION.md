# Configuration & notifications

Menina's desired state is human-editable YAML; operational state lives in
SQLite. This document covers the file layout, how app/environment definitions
merge, scaling, and outbound deploy notifications.

For the CLI, see [CLI.md](CLI.md); for the proxy/TLS, [PROXY.md](PROXY.md); for
the registry, [REGISTRY.md](REGISTRY.md); for k3s/RBAC,
[KUBERNETES.md](KUBERNETES.md).

## Layout

```text
/etc/menina/
├── config.yml
└── projects/
    └── my-platform/
        ├── project.yml
        ├── apps/{frontend,api,worker}.yml
        └── environments/
            ├── production.yml          # environment definition (optional)
            └── production/             # per-app overrides (one file per app)
                ├── api.yml
                └── web.yml

/var/lib/menina/
├── menina.db
├── registry/
├── releases/
└── backups/
```

YAML is the source of **desired** configuration; SQLite stores
indexed/resolved configuration, deployment jobs, image digests, route mappings,
release history and observed status. `menina serve` is the **single writer** for
SQLite; the proxy receives versioned routing snapshots over an authenticated
Unix socket and never writes the database.

Menina environment namespaces are derived from the project (see
[KUBERNETES.md](KUBERNETES.md#namespaces)).

## Merge rules (app + environment)

The resolver combines a shared app definition with per-environment overrides:

| Field | Rule |
| --- | --- |
| `enabled` | Environment override wins; defaults to `true` |
| `image.repository` | **Immutable** — an environment may not change it |
| `image.tag` | Environment override wins; otherwise `defaults.imageTagStrategy` (default: the environment name) |
| `workload` | Field-wise: non-zero override fields replace app fields |
| `resources` | Field-wise merge (non-empty override fields win) |
| `exposure` | Environment must **opt in**; `enabled` comes from the environment. Public exposure always requires environment-level opt-in |
| `storage` | Environment override replaces the app's storage block |
| `health` | Environment override replaces the app's health block |
| `replicas` | Environment override wins; else the app value; else platform default. Fixed count (`3`) or range (`{min, max, targetCPU}`), realized as an HPA |
| `env` | Merged; environment keys win |
| `secrets` | Merged; environment keys win. Values are secret references, never plaintext (see [Secret references](#secret-references)) |

The resolved configuration is validated as a whole **before any cluster
change**. If a file is invalid, the last valid resolved snapshot is retained and
the error is reported rather than deploying a partial update.

### Environment definition and per-app overrides

An environment may be declared two ways, and they combine:

- `environments/<env>.yml` — the environment definition (`name`, `namespace`,
  and optionally an inline `apps:` map).
- `environments/<env>/<app>.yml` — a single app's override (`enabled`,
  `image.tag`, `resources`, `replicas`, `exposure`, `env`, `secrets`, …).
  The environment's name is the directory name; the definition file is optional.

Overrides in the directory **win** over an inline `apps:` map. Splitting each
app's override into its own file lets several repositories contribute to the
same environment without ever writing the same file — the shared
`environments/<env>.yml` cannot do that safely. `menina app scale -e <env>`
writes the per-app file.

An app may declare a `source` recording the repository that owns it:

```yaml
# apps/api.yml
name: api
source:
  repo: github.com/acme/api      # blank/omitted = locally owned
  revision: 9f3c2a1              # informational
```

A blank `source` is locally/human-owned. Two non-blank `source.repo` values that
differ are a conflict: a deploying repository must refuse to overwrite an app it
does not own rather than clobbering another repository's definition.

## Scaling

`replicas` accepts a fixed count or an autoscaling range:

```yaml
replicas: 3                                     # fixed
replicas: { min: 2, max: 10 }                   # HorizontalPodAutoscaler
replicas: { min: 2, max: 10, targetCPU: 70 }    # targetCPU defaults to 70
```

A range creates (and keeps in sync) a `HorizontalPodAutoscaler` targeting the
app's Deployment with a CPU-utilization metric; a fixed count removes any stale
HPA. HPA requires the metrics API (k3s ships metrics-server).

Change it without editing YAML — baseline (all environments) or a single
environment:

```bash
menina app scale api --project my-app --replicas 4           # baseline, fixed
menina app scale api --project my-app --min 2 --max 12       # baseline, autoscaling
menina app scale api --project my-app -e production --min 3 --max 20
menina app scale api --project my-app --reset                # inherit the default
```

See the [CLI reference](CLI.md#environments--scaling) for the full model.

`menina app create` is interactive on a terminal (project, repository, port,
scaling); pass `--yes` plus flags for scripts. The platform defaults asked
during `menina setup` (namespace, replicas, CPU, memory) fill in for apps that
don't set them.

## Deploy notifications

Menina can POST a generic, provider-agnostic JSON event to an operator-supplied
receiver whenever something deploys. It does **not** speak Slack/Discord/Sentry
directly; point it at your own relay, which fans out to whatever you use.

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
`deploy.rolled_back`) are sent; `deploy.queued` is available via `events`.

> Deploy notifications (Menina → your receiver) are distinct from the registry's
> push events (Zot → Menina); see [REGISTRY.md](REGISTRY.md).

The control plane exposes Prometheus metrics at `/metrics` on the events
listener (`menina_snapshot_version`, `menina_routes`, `menina_jobs{status="…"}`).

## Secret references

Credentials are always secret references — `env:VAR`, `file:/path`, or
`literal:value` — never plaintext in `config.yml`, and they are never written
into app definitions or logged.

Apps may inject secrets as environment variables via `secrets:` (on the app
baseline or an environment override). Each entry maps an environment variable
name to a **reference**; Menina resolves it at deploy time and projects the
values into a Kubernetes Secret named `<app>-secrets`, injected with
`valueFrom.secretKeyRef`:

```yaml
# apps/api.yml
name: api
env:
  LOG_LEVEL: info
secrets:
  DATABASE_URL: file:/etc/menina/secrets/my-platform/production/database-url
  STRIPE_KEY: env:STRIPE_KEY
```

Only the reference lives in configuration; the resolved value never enters the
config tree, the resolved snapshot, or `--json` output. A missing or unreadable
reference fails the deploy rather than starting the app with an empty value.

## Config reference

`config.yml` top-level keys (defaults in parentheses):

- `host`: `hostname`, `configDir` (`/etc/menina`), `dataDir` (`/var/lib/menina`).
- `kubernetes`: `managed` (`true`), `kubeconfig`, `ingressOwnership`,
  `disableTraefik` — see [KUBERNETES.md](KUBERNETES.md).
- `registry`: see [REGISTRY.md](REGISTRY.md).
- `networking`: `mode` (`public`|`cloudflare-tunnel`), `baseDomains`,
  `publicIp`, `cloudflare` — see [PROXY.md](PROXY.md).
- `tls`: `terminate`, `mode`, `httpRedirect`, `certDir`, `acme` — see
  [PROXY.md](PROXY.md#tls).
- `platform`: `dbPath`, `namespace`, `controlSocket`, `eventsListen` (`:9000`),
  `httpListen` (`:80`), `httpsListen` (`:443`), `reconcileInterval`,
  `workers`.
- `security`: `controlTokenFile`, `trustedProxies`, `enforceProdPolicy` (`true`).
  `enforceProdPolicy` makes `app deploy`/`app rollback` require `--yes` (and the
  control plane reject unconfirmed requests) for `production`.
- `defaults`: `namespace`, `imageTagStrategy`, `replicas`, `cpu`, `memory`.
- `webhooks`: `deploy` (this document).
