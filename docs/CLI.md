# Menina CLI reference

Menina is one binary with several modes. Commands run interactively on a
terminal and exit; `menina serve` and `menina proxy` are the long-running
services.

See also: [Configuration](CONFIGURATION.md) · [Proxy](PROXY.md) ·
[Kubernetes (k3s)](KUBERNETES.md) · [Registry](REGISTRY.md) ·
[Operations](OPERATIONS.md) · [docs index](README.md).

## Conventions

- **Global flags**: `--config-dir` (env `MENINA_CONFIG_DIR`, default `/etc/menina`),
  `--json`, `--verbose`.
- **`--yes`** on any interactive/destructive command means "flags only, never
  prompt" (also the behavior when stdin is not a terminal).
- **Interactive prompts happen only on a TTY.** When stdin is not a terminal
  (piped, CI, `/dev/null`) or `--yes` is set, a missing argument is an error
  that says what is required — never a secret prompt hang.
- **Selectors** are numbered menus; **option 1 is always `+ create a new …`**
  (project / app / environment), followed by the existing items.
- Aborting a confirmation exits non-zero; success exits 0.

## Platform

| Command | Purpose |
| --- | --- |
| `menina setup [--non-interactive]` | Configure the host, registry, networking, TLS and defaults |
| `menina deploy [infra\|platform] [--dry-run]` | Reconcile k3s, storage, Zot, the control plane and the proxy |
| `menina upgrade [--check]` | Verify and install a new release, safely |
| `menina backup` | Write a consistent SQLite backup to `<data-dir>/backups/` |
| `menina install` | Install shell completion (bash/zsh/fish) |
| `menina serve` | Run the control-plane service (watcher, webhooks, deployments) |
| `menina proxy` | Run the reverse proxy |

## Observability

| Command | Purpose |
| --- | --- |
| `menina status [--json]` | Overall health (degrades gracefully when `serve` is down) |
| `menina routes [--json]` | Public hostname → Service mappings |
| `menina doctor` | Diagnose config, Kubernetes, registry, webhook and TLS |
| `menina registry status` | Zot health |
| `menina registry credential [--regenerate]` | Show or rotate the registry push credential |
| `menina notify list` | Configured deploy webhooks |
| `menina notify test [--project P] [--event E]` | Send a test deploy notification |

## Projects

| Command | Purpose |
| --- | --- |
| `menina create [project-name] [--namespace] [--description] [--environments a,b,c] [--yes]` | Create a project. Prompts the name, then asks **“Do you account for multiple environments?”** — *No* creates only `production`; *Yes* opens an editable list where you add names (shown on each step) and rename them. Then optionally offers to create an app. |
| `menina project` | Interactive: pick a project → run the app menu |
| `menina project create <name> [--namespace] [--description] [--environments a,b,c]` | Create a project and its environments (`production` by default; `--environments none` for none) |
| `menina project list [--json]` | List projects |
| `menina project delete <name> [--yes]` | Delete config (workloads pruned; PVCs/history retained) |

## Environments

| Command | Purpose |
| --- | --- |
| `menina environment` | Interactive: pick a project → pick/create an environment → show it |
| `menina environment <name> --project P` | Show an environment |
| `menina environment create <name> --project P` | Create an environment |
| `menina environment list --project P [--json]` | List environments |
| `menina environment delete <name> --project P [--yes]` | Delete an environment (workloads pruned; PVCs/history retained) |

## Apps

| Command | Purpose |
| --- | --- |
| `menina app` | Interactive: pick a project → pick/create an app → app menu |
| `menina app --project P` | Interactive: pick an app in `P` → app menu |
| `menina app <name> --project P [--environment E]` | Interactive: app menu for that app |
| `menina app create <name> [flags]` | Create an app (interactive without `--yes`) |
| `menina app list --project P [--json]` | List apps |
| `menina app deploy <name> --project P --environment E [--yes]` | Deploy and wait for the rollout |
| `menina app logs <name> --project P --environment E [--follow] [--tail N]` | Stream workload logs |
| `menina app rollback <name> --project P --environment E [--digest D] [--yes]` | Restore a recorded digest |
| `menina app scale <name> --project P [--environment/-e E \| --baseline] (--replicas N \| --min M --max X [--target-cpu C] \| --reset)` | Change scaling. `-e E` writes that environment's override; `--baseline` writes the shared baseline; on a terminal with more than one environment and neither given, it prompts. Non-interactive with neither ⇒ baseline |
| `menina app edit <name> --project P` | Edit the app YAML in `$EDITOR` |
| `menina app delete <name> --project P [--yes]` | Delete config (workload pruned; PVCs/history retained) |

`menina app create` flags: `--project`, `--repository`, `--type service|worker`,
`--port`, `--cpu`, `--memory`, `--replicas N`, `--min-replicas`/`--max-replicas`/
`--target-cpu`, `--storage`/`--mount`, `--command`, `--yes`.

### App action menu

`Deploy · Scale · Edit · Logs · Rollback · Delete · Back`. Deploy/Logs/Rollback
prompt for the environment (auto-selected when the project has exactly one).

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
