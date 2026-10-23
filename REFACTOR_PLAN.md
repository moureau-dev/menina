# Refactor plan

Status: proposal. Do not implement until reviewed and approved.

This document describes a code organization and process-boundary plan for
Menina. It does not change behavior, the CLI contract, the manifest schema, or
the public install paths. The goal is to make ownership, testing, and a future
API explicit before multi-node and team features land.

## Goals

- Make each package answer one question: desired state, operational state,
  execution, edge, API, CLI, provisioning, or published contract.
- Remove the CLI's direct dependency on the control plane internals.
- Give the API a single command path so authorization, audit, scopes, and error
  mapping have one enforcement point.
- Keep the edge independently deployable and testable.
- Keep the client cross-platform and free of host-only dependencies.
- Make a later state-store replacement and per-node edge deployment a package
  swap rather than a rewrite.
- Reduce incidental work for roadmap items that need an API, authorization,
  audit, state replacement, or per-node edge. It does not reduce intrinsic
  feature difficulty.

## Non-goals

- No microservice split over the network in this plan.
- No separate repositories or Go modules unless a real public consumer appears.
- No public HTTP API before authentication and scopes exist.
- No web dashboard in this plan.
- No self-hosted control plane, in-cluster `serve` mode, or container images and
  manifests for Menina itself. The canonical install remains the host binary.
- No empty interfaces with a single implementation "for cleanliness".
- No behavior changes, schema changes, or command changes as part of the moves.

## Target tree

```text
cmd/
  menina/                 host CLI and provisioner entrypoint
  menina-client/          standalone cross-platform client
  menina-core/            optional controller and workers
  menina-edge/            optional edge proxy

internal/
  core/
    controller/           reconciliation, releases, queue, control logic
    worker/               deploy execution, rollout, verification
    state/                store contract, sqlite implementation, migrations
    cluster/              Kubernetes client and workload operations
    config/               desired-state model, resolver, revisions
    release/              release states and recovery policy
    secrets/              secret references, files, resolution
    registry/             Zot integration, digests, events
    notify/               webhook delivery
  edge/                   HTTP/L4 proxy, TLS/ACME, routing snapshots
  api/                    transport-independent command contract
    errors/               classified errors and stable codes
    transport/            unix, http, ssh, stdio adapters
  cli/
    host/                 host commands
    client/               shared client commands
    tui/                  interactive widgets used by both CLI trees
    backup/               backup and restore commands
  provision/              k3s, systemd, storage, firewall, users, Cloudflare
  contract/               manifest parsing and versioned schema
  telemetry/              metric names, health, shared instrumentation
  version/                build metadata

tests/
  integration/            cross-package tests behind build tags
```

`menina-core` and `menina-edge` are optional binaries. Until multi-node is a
committed goal, `menina` can keep embedding those roles through subcommands.

## Package mapping

| Current | Target | Notes |
| --- | --- | --- |
| `internal/cli` | `internal/cli/host` | Host commands only |
| `internal/clientcli` | `internal/cli/client` | Shared client commands |
| `internal/tui` | `internal/cli/tui` | Only CLI consumers |
| `internal/backup` | `internal/cli/backup` | Only backup and restore commands |
| `internal/clierr` | `internal/api/errors` | Shared error contract, also used by control code |
| `internal/config` | `internal/core/config` | Desired-state model and resolver |
| `internal/database` | `internal/core/state` and `internal/core/state/sqlite` | Store contract plus implementation |
| `internal/kube` | `internal/core/cluster` | Minimal Kubernetes client |
| `internal/deployment` | `internal/core/cluster` and `internal/core/worker` | Workload operations versus deploy orchestration |
| `internal/release` | `internal/core/release` | No internal dependencies today |
| `internal/secrets` | `internal/core/secrets` | Shared by CLI, controller, and deployment |
| `internal/registry` | `internal/core/registry` | Zot stays an external workload |
| `internal/notify` | `internal/core/notify` | Used by controller and CLI |
| `internal/platform` | `internal/core/controller`, `internal/core/worker`, `internal/api` | Split by responsibility |
| `internal/proxy` | `internal/edge` | Already a leaf package |
| `internal/manifest` | `internal/contract/manifest` | Published client contract |
| `internal/schema` | `internal/contract/schema` | Versioned schema stays unchanged |
| `internal/infrastructure` | `internal/provision` | Host-only provisioning |
| `internal/version` | `internal/version` | Shared by all binaries |

The mapping is logical, not a one-to-one file move. `deployment` and `platform`
contain more than one responsibility and will be split by behavior.

## Why this shape

`internal/` is the default because Menina is an application, not a Go library.
The compiler prevents outside modules from importing these packages, so they can
be moved without creating compatibility promises. A public Go SDK, if one ever
exists, gets a narrow package or separate module at that point.

`pkg/` is not used. It does not add structure, it implies a stable public API,
and it produces longer import paths without a benefit. Go already has `cmd/`,
`internal/`, and the module root for public packages.

The important dependency direction is:

```text
contract, telemetry, version          leaves
edge, release, registry, notify       leaves
core/state, core/cluster              infrastructure adapters
core/config, core/secrets             domain
core/controller, core/worker          orchestration
api                                   transport and command dispatch
cli                                   clients of api
provision                             host lifecycle only
```

`edge` keeps no dependency on the controller or database. It receives routing
snapshots and serves traffic. Zot remains a Kubernetes workload, so there is no
Menina registry server package.

`tui` and `backup` are not top-level domain packages. They have one consumer
family, the CLI, so they live under `cli/`.

## Dogfooding and self-hosting

Dogfooding the application model is in scope as a practice:

- deploy internal apps through Menina;
- keep Zot in k3s;
- run a separate Menina instance inside k3s to manage non-critical or staging
  workloads.

Self-hosting the control plane is not the canonical install and is not part of
this plan:

- controller and provisioner stay on the host for single-node;
- the host systemd install and local break-glass path remain;
- the controller is the thing that repairs the cluster, so it must not depend on
  that cluster being healthy;
- a self-hosted controller needs external state, leader election, in-cluster
  authentication, image builds and manifests, upgrade and rollback semantics,
  and a recovery path.

The refactor makes self-hosting possible without a rewrite because it produces
`menina-core` and `menina-edge` binaries, a real API, a state interface, and
per-role telemetry. It does not make it safe. That is a separate topology plan
after Tier 3.

## Roadmap impact

This plan does not change the roadmap's scope or the intrinsic difficulty of its
items. It removes incidental integration work where an item needs a boundary
that does not exist yet. That is the difference between "implement the feature"
and "first invent the seam, then implement the feature."

| Roadmap item | Effect | What actually changes |
| --- | --- | --- |
| 17 Audit capture and query | Strong | One dispatch point records actor, action, target, plan, and source. Schema, query, and retention remain. |
| 18a Authorization model | Strong | Enforcement lives in one middleware path instead of per command. Policy design remains. |
| 38b Scope enforcement | Strong | Command, project, environment, and action checks share one dispatcher. |
| 44 HTTP API plus SSE or WebSocket | Strong | The API and transport adapters already exist. Auth and public exposure remain. |
| 30 Per-node proxy | Strong | `edge` is a separate binary and already consumes snapshots. Routing state and certificate distribution remain. |
| 27 HA control-plane state | Moderate | The store interface isolates SQLite, but replication, leader election, fencing, and idempotency are still the hard part. |
| 37a-c identity, keys, enrollment | Moderate | Tables and actor plumbing have a home. The identity model is still new work. |
| 16a and 16b scoped secrets and registry credentials | Moderate | Authorization centralizes. Storage isolation and token design remain. |
| 39 Client contexts and CI credentials | Moderate | `cli/client` owns contexts and precedence. |
| 40 In-process SSH transport | Moderate | Transport is isolated on the client side. |
| 13 Metrics depth and alert rules | Moderate | `telemetry` defines shared metric names. Alert content remains. |
| 23 Fleet overview | Moderate | API read model and metrics exist. Aggregation remains. |
| 42 CI workload identity | Moderate | The API has a transport boundary. Token exchange remains. |
| 15 Finish error taxonomy | Moderate | One error mapping point. Coverage and codes remain. |
| 19a SSO and OIDC | Small | API middleware helps. SSH certificate issuance is separate. |
| 28 Multi-node k3s provisioning | Small | `provision` is better contained. Node lifecycle work is unchanged. |
| 43 Registry token service | Small | Registry integration is isolated. Token service design remains. |
| 45 Per-tenant rate limits | Small | The API is a natural enforcement point, but quotas and fairness remain. |
| 14, 29, 32, 33, 34, 19b, 19c, 25 | None | Storage, DR, supply chain, canary, quotas, and network policy are domain and operations work. |

How to read the difficulty numbers:

- The scores in `ROADMAP.md` are end-to-end. Do not lower them when this plan
  lands.
- Expect roughly one point less incidental work for moderate items, one to two
  for the strong boundary-heavy items, and zero for domain and operations items.
- Update the roadmap only after a phase lands and has evidence, not when the
  folder tree changes.
- The main saving is avoiding the same API, auth, audit, and edge work being
  built twice.

Ordering implications:

- Extract the API before Tier 2 identity, so scopes and audit have one path.
- Introduce the state interface before Postgres and HA, so the store can change.
- Split `edge` before per-node proxy, so the data plane can move.
- Add transports after identity, not before.

## API boundary

`internal/api` owns the command contract, not the domain logic.

- Versioned request and response types, JSON, and stable error envelopes.
- Streaming for jobs and logs, using NDJSON now and SSE or WebSocket later.
- One dispatcher for every transport. Transports only decode the request,
  establish the actor, encode the response or stream, and call the dispatcher.
- Middleware for server-derived actor, scope checks, audit capture, idempotency,
  base state token checks, and classified errors.
- Handlers call `core/controller` and `core/worker`; they do not contain
  reconciliation or Kubernetes logic.
- No raw SQL, no raw Kubernetes passthrough, and no client-supplied actor.

The API is the single mutation path for running state. Plan generation may stay
client-side for preview, but execution is revalidated server-side against the
base state token, file hashes, and ownership.

### Transports

```text
internal/api/
  contract.go        request, response, event, and error types
  server.go          actor, authz, audit, idempotency, dispatch
  client.go          client used by the CLI
  transport/
    unix/            net/http over the Unix socket
    http/            TCP or TLS listener, later
    ssh/             forced-command bridge, later
    stdio/           optional NDJSON for tests
```

Transport rules:

- Start with the Unix socket. HTTP, SSH, and stdio adapters are added only when a
  real client needs them.
- Do not define a generic `Transport` interface with `Send` and `Receive`.
  Streaming, TTY, stdin secrets, cancellation, and error semantics differ per
  transport. Use concrete adapters around one `api.Server`.
- Actor derivation is per transport: Unix peer credentials plus a local token,
  HTTP sessions or bearer tokens or OIDC, SSH authenticated key mapping.
- Capabilities can differ. An adapter may support streams but not a TTY, and it
  must say so in the contract.
- The API is not an app route and is not served through the public proxy. The
  proxy does no authentication, authorization, or rate limiting. The API gets a
  Unix socket, an SSH bridge, or a dedicated listener with its own TLS and auth.
- If a future UI exists, expose the UI. Its backend talks to the API over
  loopback or a private network. Tenant workloads must not reach the API.
- Every transport runs the same contract test matrix.

## State and schema

The refactor is schema-neutral. Package moves and process splits do not change
the database file, tables, or migrations.

- Keep one logical control-plane database. Do not give each module its own
  SQLite file.
- One connection owner (`core/state/sqlite`) and one migration runner. Packages
  never call `sql.Open` themselves.
- Module ownership is expressed through repositories and table ownership, not
  separate databases.
- Transactions span modules when the invariant spans them. A job completing, a
  release becoming active, and an event being recorded are one logical change.
- Move migrations without changing their identity. The filename is the version
  recorded in `schema_migrations`, so `0001` through `0005` must keep their names
  and order. Renaming, renumbering, squashing, or editing applied migrations is
  forbidden.
- Add a test that opens a database created before the refactor and verifies it
  migrates cleanly.
- New migrations (`0006+`) are for new features: audit, identity and keys, API
  sessions or tokens, idempotency if HTTP clients retry, and leader or job
  leases. They are not part of the moves.
- Postgres later means one logical database with schemas such as `core` and
  `audit`, plus a separate migration stream. It does not mean a database per
  module.

Separate storage is justified only for genuinely different data:

- secrets and desired state stay as files
- Zot keeps its own registry data
- metrics and logs stay external
- analytics, if ever added, is separate and loss-tolerant

## CLI to API

Not every command should use the API. Bootstrap and diagnosis must work when the
control plane is down.

| Command group | Talks to | Reason |
| --- | --- | --- |
| `app`, `release`, `job`, `secret` | API | Mutations and streams need one enforcement point |
| `config revisions`, `config rollback` | API | Operational state, not local files |
| `notify`, `registry`, `routes` | API | Host state and delivery history |
| `status`, `doctor` | API first, local fallback | Diagnosis must work when `serve` is down |
| `apply`, `promote` | API for execution | Client may preview, server revalidates and writes |
| `config validate` | Local | Pure resolution and validation |
| `init`, `validate`, `build`, `push`, `schema`, `capabilities` | Local | Repository or client work |
| `setup`, `deploy`, `upgrade`, `install`, `restore`, `backup` | Local packages | Root lifecycle, may run before or without the API |
| `serve` | Runs the API | Control plane entrypoint |
| `proxy` | Runs the edge | Data plane entrypoint |

Remote clients keep SSH as the transport: client to forced command, then forced
command to the local API. A future UI or agent uses HTTP/TLS to the same API on
a dedicated listener. The API is never an app route through the edge proxy.

The CLI must never write config or SQLite directly for online operations. The
forced command should map allowlisted arguments to API requests instead of
execing the host CLI, so there is one parser and one actor derivation.

## Testing

Each package owns co-located tests.

- Same-package tests cover unexported behavior.
- `package foo_test` tests cover the exported contract.
- `internal/testutil` holds shared fixtures, never production exports.
- `internal/core/state` defines a store contract test suite. SQLite runs it now;
  a later Postgres implementation runs the same suite.
- `internal/core/state/sqlite` tests migration of a database created before the
  refactor, with the original `schema_migrations` versions.
- `internal/api` tests dispatch and middleware without starting systemd or k3s.
- `internal/api/transport/*` runs the same request matrix per adapter, including
  actor derivation and streaming.
- `internal/core/controller` uses a fake store and fake cluster.
- `internal/core/worker` tests job claiming, idempotency, and failure paths.
- `internal/edge` tests routing, TLS, snapshot swaps, and upstream errors.
- `internal/contract` keeps the schema reflection contract test.
- `internal/cli` tests flag and environment precedence only, not domain behavior.
- Cross-package tests stay under `tests/` behind `//go:build integration`.

These commands remain the gate:

```bash
go test ./...
go test -race ./...
go vet ./...
gofmt -l ./cmd ./internal ./tests
go build ./...
```

Real k3s and failed-deployment tests remain explicit and are not replaced by
unit tests or documentation.

## Migration phases

1. Add package mapping and dependency rules. No code moves.
2. Move leaf packages: `edge`, `contract`, `telemetry`, `version`. Update imports
   only.
3. Move `tui` and `backup` under `cli/`; split `cli` and `clientcli`.
4. Split `platform` into `controller`, `worker`, and `api`; move `clierr` under
   `api/errors`.
5. Move state, cluster, config, secrets, release, registry, and notify into
   `core/`. Move migrations unchanged and keep their filenames. Introduce
   consumer-side interfaces only where testing or a second implementation
   requires them.
6. Route online CLI commands through `api`; remove `cli` to `platform` imports.
   Serve the API over the Unix socket only.
7. Add optional `menina-core` and `menina-edge` binaries. Keep the `menina`
   umbrella binary unless a split proves necessary.
8. Add another transport only when a client needs it. HTTP before a UI, SSH
   bridge when remote clients should stop execing the host CLI. Each adapter
   must pass the shared contract tests.
9. Only after these steps, start Tier 3 state replication, leader election, and
   per-node edge work.

Each phase must keep `go test ./...`, `go test -race ./...`, `go vet ./...`, and
`go build ./...` green. Client cross-compilation must keep working at every
phase.

## Acceptance criteria

- No behavior change in the CLI, manifests, schema URL, installers, or deploy
  pipeline.
- `internal/cli` does not import controller or worker packages.
- `internal/edge` imports no controller, API, or state package.
- The API is the only mutation path for running state.
- The API is not an app route through the edge proxy.
- One control-plane database and one migration stream remain. No per-module
  SQLite files.
- Existing `schema_migrations` versions and migration filenames are unchanged.
- Every package has a clear owner and dependency direction.
- Unit tests run without k3s, Docker, or a network.
- Integration and end-to-end coverage remains available behind build tags and
  scripts.
- `docs/STRUCTURE.md` is updated in the same change that moves packages.

## Risks

- Import cycles during the `deployment` and `platform` split. Mitigate by moving
  one responsibility at a time and compiling after each move.
- Interface bloat. Add an interface at the consumer only when a test fake or a
  second implementation exists.
- API scope creep. The first API version exposes existing behavior only. Auth
  and public exposure wait for Tier 2 identity.
- Transport proliferation. Each new transport adds an auth model and streaming
  edge cases. Add one only for a real client and run the shared contract tests.
- Schema drift. Moving migrations is safe only if filenames and versions are
  unchanged. Any new table is a separate feature migration.
- Churn and merge conflicts. Move leaf packages first, keep each phase small.
- More binaries increase packaging and systemd surface. Keep `menina` as the
  umbrella until multi-node requires otherwise.
- Distributed monolith. Do not add network hops between CLI and core before the
  API contract, actor derivation, and scopes exist.

## Open questions

- When should HTTP move from a private listener to a remote listener? This
  requires identity, scopes, and audit first.
- Should `apply` compute the plan client-side or always on the host through the
  API?
- Does `backup` remain fully local, or gain an API path for scheduled and remote
  use?
- When does a public Go client or protocol module become justified?
- Does the edge need a separate release cadence from the controller, or is one
  binary with subcommands enough?
- After this plan, which role is first to run inside k3s: edge, workers, or a
  staging controller? A self-hosted production controller needs its own plan.
