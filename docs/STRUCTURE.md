# Repository structure

Menina has a host CLI binary, a cross-platform client binary, optional role
binaries, and shared packages that must stay portable.

```text
cmd/
  menina/                 host CLI and provisioner entry point
  menina-client/          standalone cross-platform client entry point
  menina-core/            optional controller and workers entry point
  menina-edge/            optional edge proxy entry point

internal/
  api/                    command contract, dispatcher, client, routing subscription
    errors/               classified errors and stable exit codes
    transport/            unix, http, ssh, and stdio adapters
  cli/
    host/                 host commands, prompts, TUI menus
    client/               shared client commands (init/validate/apply/build/push, secrets, jobs, config, meta)
    tui/                  Bubble Tea widgets and badges
    backup/               portable backup/restore commands and helpers
  contract/
    manifest/             .menina.yml parsing, interpolation, rendering, plans
    schema/               embedded versioned manifest JSON Schema
  core/
    cluster/              Kubernetes workloads, PVCs, services, logs, rollout
      kube/               minimal Kubernetes API client
    config/               desired-state model, loader, resolver, validation, revisions, lock, state token
      plan/               reviewed apply plans and server-side plan execution
    identity/             profiles, scopes, keys, expiry, status, last seen
    controller/           control plane, reconciler, HTTP command handlers
    notify/               deploy webhook delivery (HMAC, retries, delivery id)
    registry/             Zot client, digest resolution, push events, bundled resolver
    release/              per-app release states and recovery policy
    secrets/              secret refs, file writing, resolution
    state/
      contract/           operational state interface used by controller and worker
      contracttest/       shared contract test suite for store implementations
      fake/               in-memory store double for controller tests
      sqlite/             SQLite jobs, releases, routes, events, observed state
        migrations/       ordered SQLite migrations
    worker/               deploy job execution, rollout, verification
  edge/                   HTTP/L4 proxy, routing snapshots, TLS
  provision/           k3s, systemd, storage, firewall, users, cloudflare
  telemetry/              metric names and shared instrumentation constants
  version/                build version metadata

deploy/                   systemd units and Zot configuration/assets
docs/                     public documentation and this structure guide
examples/                 example manifests and project config
scripts/                  installers and e2e scripts
tests/                    cross-package/e2e tests
website/                  marketing site and docs sync/deploy source
```

## Package conventions

- Go packages live under `internal/`; keep host-only dependencies out of
  `cli/client`, `contract/manifest`, and `contract/schema`.
- Tests are co-located as `*_test.go`.
- SQLite migrations are ordered files under
  `internal/core/state/sqlite/migrations/`. Their filenames are the recorded
  schema versions and must not be renamed or renumbered.
- Host commands live in `internal/cli/host`; shared client commands live in
  `internal/cli/client`. Do not duplicate command logic between them.
- Manifest translation belongs in `internal/contract/manifest`, not in CLI
  handlers.
- The canonical manifest schema is
  `internal/contract/schema/menina.schema.json`; the website publishes it at
  `/schema/v1/menina.schema.json` with an unversioned alias.
- The API is the mutation path for running state. The CLI uses it; the API
  handlers call controller and worker operations.
- Top-level docs (`README.md`, `CHANGELOG.md`, `ROADMAP.md`, `llms.txt`,
  `AGENTS.md`) stay at the repository root.

## When to update this file

Update this document when adding, removing, renaming, or moving:

- a top-level directory or entry point
- an `internal/` package
- a major command group or generated artifact
- the manifest/config/schema layout
