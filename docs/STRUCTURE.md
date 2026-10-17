# Repository structure

Menina has one Linux host binary, one cross-platform client binary, and shared
packages that must stay portable.

```text
cmd/
  menina/                 host CLI entry point
  menina-client/          standalone cross-platform client entry point

internal/
  backup/                 portable backup/restore (DB, config, revisions, opt-in volumes/images)
  cli/                    host CLI commands, prompts, TUI, SSH/TUI menus
  clientcli/              shared client commands (init/validate/apply/build/push, secrets, jobs, config, meta)
  clierr/                 classified CLI errors and stable exit codes
  config/                 desired-state model, loader, resolver, validation, revisions, lock, state token
  database/               SQLite jobs, releases, routes, events, observed state
    migrations/           ordered SQLite migrations
  deployment/             Kubernetes workloads, PVCs, services, logs, rollout
  infrastructure/         k3s, systemd, storage, firewall, users, cloudflare
  kube/                   minimal Kubernetes API client
  manifest/               .menina.yml parsing, interpolation, rendering, plans, schema contract tests
  notify/                 deploy webhook delivery (HMAC, retries, delivery id)
  platform/               control plane, job workers, reconciler, control socket
  proxy/                  HTTP/L4 proxy, routing snapshots, TLS
  registry/               Zot client, digest resolution, push events, bundled resolver
  schema/                 embedded versioned manifest JSON Schema
  secrets/                secret refs, file writing, resolution
  tui/                    Bubble Tea widgets and badges
  version/                build version metadata

deploy/                   systemd units and Zot configuration/assets
docs/                     public documentation and this structure guide
examples/                 example manifests and project config
scripts/                  installers and e2e scripts
tests/                    cross-package/e2e tests
website/                  marketing site and docs sync/deploy source
```

## Package conventions

- Go packages live under `internal/<area>/`; keep host-only dependencies out of
  `clientcli`, `manifest`, and `schema`.
- Tests are co-located as `*_test.go`.
- SQLite migrations are ordered files under `internal/database/migrations/`.
- Host commands live in `internal/cli`; shared client commands live in
  `internal/clientcli`. Do not duplicate command logic between them.
- Manifest translation belongs in `internal/manifest`, not in CLI handlers.
- The canonical manifest schema is `internal/schema/menina.schema.json`; the
  website publishes it at `/schema/v1/menina.schema.json` with an unversioned
  alias.
- Top-level docs (`README.md`, `CHANGELOG.md`, `ROADMAP.md`, `llms.txt`,
  `AGENTS.md`) stay at the repository root.

## When to update this file

Update this document when adding, removing, renaming, or moving:

- a top-level directory or entry point
- an `internal/` package
- a major command group or generated artifact
- the manifest/config/schema layout
