# Menina CLI reference

Menina is one binary with several modes. Commands run interactively on a
terminal and exit; `menina serve` and `menina proxy` are the long-running
services.

See also: [Configuration](CONFIGURATION.md) · [Proxy](PROXY.md) ·
[Kubernetes (k3s)](KUBERNETES.md) · [Registry](REGISTRY.md) ·
[Operations](OPERATIONS.md) · [docs index](README.md).

## Conventions

- **Global flags**: `--config-dir` (env `MENINA_CONFIG_DIR`, default `/etc/menina`),
  `--json`, `--verbose`, `--non-interactive`.
- **`--yes`** on any interactive/destructive command means "flags only, never
  prompt" (also the behavior when stdin is not a terminal).
- **Interactive prompts happen only on a TTY.** When stdin is not a terminal
  (piped, CI, `/dev/null`), `--yes` is set, or `--non-interactive` is passed, a
  missing argument is an error that says what is required — never a secret
  prompt hang. `--non-interactive` also makes a bare `menina` fail instead of
  opening the TUI.
- **Selectors** are numbered menus; **option 1 is always `+ create a new …`**
  (project / app / environment), followed by the existing items, and a final
  **`Exit`**. The app action menu ends with **`Back`**.
- **Ctrl+C always works**, including mid-prompt: it exits immediately (the first
  SIGINT also triggers graceful shutdown of `serve`/`proxy`).
- Aborting a confirmation exits non-zero; success exits 0.
- **`delete` is a soft delete by default.** The target stops (its workload is
  scaled to zero) and its config is kept for `restore`; `--hard` removes the
  config and prunes the workload. PersistentVolumeClaims and release history are
  retained either way. Soft deletion is recorded on the target's `observed` row
  (`deleted_at`); `deleted_at IS NULL` means active.

## Automation errors

With `--json`, failures are written to stderr as a stable envelope:

```json
{"error":{"code":"validation","message":"...","hint":"..."}}
```

Exit codes: `1` generic, `2` validation, `3` not found, `4` auth, `5` timeout,
`6` conflict, `7` unsupported. Not every command is classified yet; unwrapped
errors use the generic code/exit.

## Platform

| Command | Purpose |
| --- | --- |
| `menina setup [--non-interactive]` | Configure the host, registry, networking, TLS and defaults |
| `menina deploy [infra\|platform] [--dry-run]` | Reconcile k3s, storage, Zot, the control plane and the proxy |
| `menina upgrade [--check]` | Verify and install a new release, safely |
| `menina backup [--out FILE] [--with-secrets] [--with-volumes] [--with-images] [--consistent] [--passphrase]` | Create a portable `tar.gz` bundle (DB + config + projects; opt-in secrets, certs, app volumes, registry images) |
| `menina restore <archive> [--force] [--with-volumes] [--with-images] [--consistent] [--passphrase]` | Restore a `menina backup` archive |
| `menina install` | Install shell completion (bash/zsh/fish) |
| `menina serve` | Run the control-plane service (watcher, webhooks, deployments) |
| `menina proxy` | Run the reverse proxy |

## Observability

| Command | Purpose |
| --- | --- |
| `menina status [--json]` | Overall health (degrades gracefully when `serve` is down) |
| `menina routes [--json]` | Public hostname → Service mappings (and raw L4 TCP/UDP exposures) |
| `menina doctor` | Diagnose config, Kubernetes, registry, webhook and TLS |
| `menina registry status` | Zot health |
| `menina registry credential [--regenerate]` | Show or rotate the registry push credential |
| `menina app status <name> --project P --environment E [--json]` | Last deployment job, release digest and observed phase for an app |
| `menina job list [--status S] [--limit N] [--json]` | List deployment jobs, newest first |
| `menina job watch <id> [--json] [--timeout D]` | Stream NDJSON job snapshots until a terminal state |
| `menina job cancel <id>` | Cancel a pending or processing deployment job |
| `menina config validate [--json]` | Resolve the whole config tree and validate it without applying (non-zero exit when invalid) |
| `menina config revisions [--project P] [--environment E] [--json]` | List pre-apply configuration snapshots |
| `menina config rollback <id> --yes` | Restore files changed by an apply revision (desired state only) |
| `menina schema [--json] [--url]` | Print the embedded `.menina.yml` JSON Schema, or only its public URL |
| `menina capabilities [--json]` | Local version, OS/arch, manifest schema version, feature flags and command list |
| `menina notify list` | Configured deploy webhooks |
| `menina notify test [--project P] [--event E]` | Send a test deploy notification |

## Projects

| Command | Purpose |
| --- | --- |
| `menina create [project-name] [--namespace] [--description] [--environments a,b,c] [--yes]` | Create a project. Prompts the name, then asks **“Do you account for multiple environments?”** — *No* creates only `production`; *Yes* opens an editable list where you add names (shown on each step) and rename them. Then optionally offers to create an app. |
| `menina project` | Interactive: pick a project → run the app menu |
| `menina project create <name> [--namespace] [--description] [--environments a,b,c]` | Create a project and its environments (`production` by default; `--environments none` for none) |
| `menina project get <name> [--json]` | Show a project's namespace, apps and environments |
| `menina project list [--json]` | List projects |
| `menina project delete <name> [--hard] [--yes]` | Soft-delete (default): workloads stop (scaled to zero), config kept for restore. `--hard`: remove config and prune workloads. PVCs/history retained either way |
| `menina project restore <name>` | Restore a soft-deleted project |

## Environments

| Command | Purpose |
| --- | --- |
| `menina environment` | Interactive: pick a project → pick/create an environment → show it |
| `menina environment <name> --project P` | Show an environment |
| `menina environment create <name> --project P` | Create an environment |
| `menina environment list --project P [--json]` | List environments |
| `menina environment delete <name> --project P [--hard] [--yes]` | Soft-delete (default): workloads stop (scaled to zero), config kept. `--hard`: remove config and prune workloads. PVCs/history retained |
| `menina environment restore <name> --project P` | Restore a soft-deleted environment |

## Apps

| Command | Purpose |
| --- | --- |
| `menina app` | Interactive: pick a project → pick/create an app → app menu |
| `menina app --project P` | Interactive: pick an app in `P` → app menu |
| `menina app <name> --project P [--environment E]` | Interactive: app menu for that app |
| `menina app create <name> [flags]` | Create an app (interactive without `--yes`) |
| `menina app list --project P [--json]` | List apps |
| `menina app deploy <name> --project P --environment E [--yes] [--no-wait] [--timeout D] [--verify]` | Deploy and wait for the rollout (print job id + config snapshot version). `--no-wait` queues and returns; `--verify` checks public HTTP routes and TCP L4 ports after the rollout |
| `menina app status <name> --project P --environment E [--json]` | Last job, release digest and observed status |
| `menina app reset-volume <name> --project P --environment E [--yes]` | Scale to zero, delete the PVC, and redeploy so the volume is recreated empty (data is lost) |
| `menina app logs <name> --project P --environment E [--follow] [--tail N] [--since D]` | Stream workload logs, bounded by tail lines and/or age |
| `menina app rollback <name> --project P --environment E [--digest D] [--yes] [--no-wait] [--timeout D]` | Restore a recorded digest |
| `menina app scale <name> --project P [--environment/-e E \| --baseline] (--replicas N \| --min M --max X [--target-cpu C] \| --reset)` | Change scaling. `-e E` writes that environment's override; `--baseline` writes the shared baseline; on a terminal with more than one environment and neither given, it prompts. Non-interactive with neither ⇒ baseline |
| `menina app edit <name> --project P` | Edit the app YAML in `$EDITOR` |
| `menina app delete <name> --project P [--hard] [--yes]` | Soft-delete (default): stop the workload (scale to zero), keep config for restore. `--hard`: remove config and prune the Deployment/Service/routes. PVCs/history retained |
| `menina app restore <name> --project P` | Restore a soft-deleted app |

`menina app create` flags: `--project`, `--repository`, `--type service|worker`,
`--port`, `--cpu`, `--memory`, `--replicas N`, `--min-replicas`/`--max-replicas`/
`--target-cpu`, `--storage`/`--mount`, `--command`, `--yes`.

### App action menu

`Deploy · Scale · Edit · Logs · Rollback · Delete · Back`. Deploy/Logs/Rollback
prompt for the environment (auto-selected when the project has exactly one).

## Secrets

| Command | Purpose |
| --- | --- |
| `menina secret set <name> --project P --environment E [--file F]` | Store a secret value at `secrets/P/E/<name>` (read from stdin — hidden on a terminal — or `--file`). Prints the `file:` reference to use in `apps/*.yml` |
| `menina secret rotate <name> --project P --environment E [--file F] [--generate]` | Replace a secret value from stdin/file, or generate a random one |
| `menina secret list --project P --environment E [--json]` | List secret names (values are never shown) |
| `menina secret remove <name> --project P --environment E [--yes]` | Delete a stored secret value |
| `menina secret verify [name] --project P --environment E [--json]` | Check that secret files exist, are readable and are not empty |

Secret files are created with mode `0660`; the environment directory is `0770`
with the setgid bit. Run the CLI as a user in the `menina` group (or root) so the
control plane can read them. Do not copy files into `secrets/` manually — use
`menina secret set`; a manual copy commonly produces a permission denied when
the control plane later reads it. `menina secret verify` checks existence,
readability and non-empty content without printing values.

Values are never taken from arguments, printed, or written into configuration;
apps reference them by name:

```bash
printf '%s' "$DATABASE_URL" | menina secret set DATABASE_URL --project my-app --environment production
# then in projects/my-app/apps/api.yml (or an env override):
#   secrets: { DATABASE_URL: "file:/etc/menina/secrets/my-app/production/DATABASE_URL" }
```

At deploy time Menina resolves the reference and projects it into a Kubernetes
Secret named `<app>-secrets`, injected as env vars via `valueFrom.secretKeyRef`.
See [CONFIGURATION.md](CONFIGURATION.md#secret-references).

## Environments & scaling

An **environment** is a deployment target (`production`, `staging`, …). It starts
empty and holds **per-app overrides** (`enabled`, `image`, `resources`,
`replicas`, `exposure`, `env`, …). You deploy an app *into* an environment:

```bash
menina app deploy api --project my-app --environment production
```

**Scaling is environmental.** The app file holds the baseline (`replicas`), and
each environment may override it:

```
projects/my-app/apps/api.yml            # baseline: replicas: 1
projects/my-app/environments/prod.yml   # override: replicas: {min: 2, max: 10}
```

`menina app scale` targets the **baseline** by default; pass `--environment/-e`
for a specific environment, or `--baseline` to be explicit. **On a terminal**,
if the project has more than one environment and you give neither, it prompts:

```
Apply scaling to
  1. All environments (baseline)
  2. production
  3. staging
Select [1]:
```

```bash
menina app scale api --project my-app --min 2 --max 10                # baseline (prompts on a TTY)
menina app scale api --project my-app --baseline --min 2 --max 10     # baseline, no prompt
menina app scale api --project my-app -e production --min 2 --max 10  # production only
menina app scale api --project my-app -e production --reset           # drop the override
```

The app action menu (Scale) uses the same prompt. The effective value for an
environment is the override if present, else the baseline, else the platform
default.

## Version & help

- `menina version`, `menina --version`, `menina -v`
- `menina help [command]`, `menina <command> --help`

## Examples

```bash
# New here: create a project, then deploy an app to it
menina create my-app                 # asks: multiple environments? (No = just production)
menina app --project my-app          # pick an app → Deploy

# Non-interactive / CI
menina create my-app --yes --environments development,staging,production
menina app create api --project my-app --repository registry.example.com/my-app/api --port 8080 --yes
menina app deploy api --project my-app --environment production --yes

# Scale without editing YAML
menina app scale api --project my-app --min 2 --max 10
menina app scale api --project my-app --replicas 4
menina app scale api --project my-app --reset
```
