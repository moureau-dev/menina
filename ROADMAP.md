# Roadmap

Menina is an agent-friendly, single-node application platform. The current
release line is deliberately strong for one operator or a small trusted team on
one server. This file separates what is done from what is still missing, and
makes the multi-node/HA work explicit instead of hiding it under “HA/DR”.

## Positioning

- **Strong today:** deterministic CLI/agent contract, manifests, apply plans,
  SSH workflow, registry push-to-deploy, rollback by digest, single-node k3s.
- **Not yet:** multi-team identity/authorization, multi-node availability, real
  observability/alerting, supply-chain controls.
- **Design bias:** one small system you can understand and operate yourself.
  Add capabilities without turning Menina into a generic Kubernetes distro.
- **Identity rule:** credentials and identity belong to the execution context
  (client, CI job, operator), never to the manifest. `.menina.yml` names the
  target project; it never selects who is acting. `source.repo` stays an
  ownership hint, not an authorization boundary.

## Landed recently (through v0.1.1)

- Repository manifests (`.menina.yml`), client installers, manifest schema,
  `init`/`validate`/`apply`/`build`/`push`, `--json`, `--non-interactive`.
- Apply as upsert with `--diff`/`--dry-run`, `--verify`, source ownership,
  `--prune` via reviewed plans.
- Concurrency: canonical `stateToken`, config-dir lease lock with fencing,
  `--if-state-token`, plan IDs with base/file-hash verification.
- Config revisions with actor/source/plan/hash metadata, rollback, backup/restore
  inclusion, configurable retention.
- Secrets: set/list/remove/rotate/verify/references, explicit `--restart`.
- Deploy `--verify` (reachability), `app logs --since/--tail`, job watch stream,
  client job passthrough over SSH.
- Versioned manifest schema (`/schema/v1/...`), capabilities/compatibility,
  classified errors with stable exit codes.
- Host/client TUI, cross-platform client builds and installers.
- Operational state contract with shared contract tests, an in-memory fake for
  controller tests, and an existing-database migration test.
- Per-app release states, pinned job configuration, verification, hooks,
  recovery policies, digest promotion, secret-staleness reporting, scheduled
  backups, metrics/alert guidance, and probe-timing hardening (v0.1.0/v0.1.1).

Guiding principle: **make failure paths as deliberate and predictable as the
happy path.** A release must be identifiable, verifiable, recoverable, and
honest about what was and was not rolled back.

## Tier 1 — release model, determinism, and operations (landed)

Tier 1 landed in v0.1.0 and v0.1.1. It is recorded here as the baseline the
higher tiers build on, not as remaining work.

- **App releases** have pinned image and resolved configuration (`stateToken`
  plus a stored config snapshot), verification and migration status, a
  previous-known-good pointer, and explicit states (`prepared`, `migrating`,
  `deploying`, `verifying`, `active`, `failed`, `recovering`, `rolled_back`).
- **Environment releases** are aggregates. `menina release status` reports each
  component and never hides a failed app behind a global success, with code,
  configuration, and data state reported separately.
- **Recovery** is an explicit per-app `previous` / `none` / `require-approval`
  policy with separate triggers (`deploy-error`, `verification-failure`,
  `timeout`, `interrupted`). An image restore never claims to reverse a
  migration; apps with migrations default to `require-approval`.
- **Verification** supports expected HTTP status/body/header and a smoke-test
  Job, with an explicit `recover` / `keep` failure policy.
- **Promotion** moves an existing digest between environments without rebuilding,
  using a reviewed target plan and state token.
- **Hooks** (`preDeploy`, `migrate`, `postDeploy`) are opt-in, permissioned via
  `security.allowAppHooks`, image-pinned, timeout-bounded, logged, and never
  assumed reversible. A one-off Job primitive backs hooks and smoke tests.
- **Ordering** is the minimal explicit `dependsOn` chain; there is no general
  DAG. Optional CSI volume snapshots are opt-in and fail explicitly when the
  storage class cannot provide them.
- **Determinism and operations**: deployment jobs pin the resolved config
  snapshot; Deployments are annotated with an injected-secret hash and
  `app status` reports stale rollouts after a rotation; job watch has a resume
  cursor and a terminal sentinel carrying the remote exit code; webhook
  deliveries are stored with idempotency keys and can be replayed; metrics cover
  deploy duration/outcome, queue depth, replicas, proxy readiness, and node/disk
  capacity; scheduled daily backups and a CI restore round trip exist; control
  plane errors are classified for CLI exit codes.

Exit criteria are met: releases are pinned and identifiable; a failed deployment
reports code, config, and data state separately; verification has a failure
policy; promotion does not rebuild; deploys cannot execute against config written
after queueing; and a failed deploy is observable without manual checks.

## Tier 2 — API contract, team, and security

**Shared-access gate:** before multiple independent clients or autonomous agents
share one host, require project-scoped credentials and attributable audit logs.
A shared administrative identity is not a tenant boundary. RBAC/SSO can follow.

The work is split into independently shippable pieces. Item 46 provides the
command path that items 17, 18a, 38b, and 44 attach to, so land 46 before 38b;
47 follows 46. Items 37a-38b must land together: identity without enforcement is
not a security boundary.

### API contract and desired state

1. **46 API command contract.** One dispatcher and versioned request/response
   types for every transport; server-derived actor context; idempotency keys;
   shared transport parity tests; optional stdio adapter. Handlers move behind
   the dispatcher.
2. **47 API-first desired-state mutations.** Route `apply`,
   `config revisions`, and `config rollback` through the API with server-side
   base-state token and file-hash revalidation. Local inspection may remain for
   diagnostics.

### Client identity and access

3. **37a Identity/profile model + storage.** Name, scopes, attached keys,
   expiry, status, and last seen. The server derives the profile from the
   authenticated key; the client never asserts its own identity.
4. **37b Enrollment token lifecycle.** One-time token, short TTL, hashed at
   rest, consumed transactionally, never reprinted.
5. **37c Key lifecycle.** Rotate, revoke, list, display fingerprints, with
   optional short TTL / SSH certificates.
6. **38a Key-derived actor and forced-command wrapper.** Map the authenticated
   key to a profile, allowlist `menina` subcommands, inject the actor
   server-side, and have remote clients use the API bridge instead of execing
   the host CLI.
7. **38b Scope enforcement.** Check scopes against the requested command,
   project, environment, and action.
8. **39 Client contexts and CI credentials.** `--profile` / `MENINA_PROFILE`
   over stored contexts, flag/env precedence, host-key pinning, `whoami`.
9. **41 One-paste bootstrap.** `setup` prints the reachable host, host-key
   fingerprint, and enrollment command; the installer verifies with `whoami`.
10. **40 In-process SSH transport.** Remove the `openssh-client` dependency and
    pin host keys by fingerprint.

### Secrets and registry

11. **16a Project-scoped secret permissions.**
12. **20 Secret storage isolation** beyond the shared `menina` group.
13. **16b Per-project/client registry credentials**, preferring short-lived
    registry tokens over a shared htpasswd.

### Audit and authorization

14. **17 Audit capture and query.** Actor, key, action, target, plan/state
    token, source repo, and timestamps.
15. **18a Authorization model and enforcement** beyond secrets and registry.
16. **18b Ownership enforcement.** `source.repo` becomes a check, not a
    boundary.
17. **21 Multi-client readiness gate.**

Identity is selected by the execution context, not the manifest. Client-side
selection is `--profile` / `MENINA_PROFILE` over a stored context, with
`--host` / `MENINA_HOST` layered on top; `menina whoami` reports the
server-derived profile and scopes. CI uses an enrolled key materialized from its
secret store, pins the host key, and runs with prompts disabled.

Exit criteria: multiple operators can share a host without sharing credentials
or silently overwriting each other; every action is attributable to a client,
key, and profile; scopes are enforced by the host, not just recorded; and
revoking one client does not disturb the others.

## Tier 2b — larger-team controls (deferred)

These do not block the shared-access gate; they assume independent clients
already have identity, scopes, and audit.

- **19a SSO/OIDC.** Issue short-lived SSH certificates for humans and CI.
  HTTP/JWT only when an API lands.
- **19b Per-team quotas.**
- **19c NetworkPolicy isolation.**
- **42 CI workload identity.** OIDC federation (for example GitHub Actions)
  exchanges a workload token for a short-lived client credential.
- **43 Registry token service.** Docker token auth with per-project scopes and
  a short TTL, replacing shared htpasswd credentials.
- **45 Per-tenant rate limits and fairness.** Bound registry pulls, job
  throughput, and concurrency per tenant so one client cannot starve the rest.

## Tier 2c — optional interfaces (product-gated)

- **44 HTTP API + WebSocket/SSE.** Stateless JSON API and streams for a browser
  or hosted surface. Not required for multi-tenancy; keep SSH as the supported
  CLI and break-glass path.

## Tier 3 — fleet operations, multi-node, and HA/DR

### Fleet operations

These are useful even while staying single-node:

- edge request metrics and opt-in redacted access logs: requests by route,
  status class, latency, upstream errors, in-flight requests, certificate
  expiry, and snapshot age (item 49);
- per-client/per-project backup schedules and retention;
- aggregate overview of failed deployments, unhealthy apps, disk usage,
  expiring certificates, and backup failures;
- a tested replacement-host workflow: move a project, its registry state,
  revisions, secrets, and volumes to a new server;
- resource limits and quotas per project;
- a deliberate answer for multi-tenant vs one-installation-per-client isolation.

### Multi-node and HA


**Multi-node is a huge win, but it is a program, not a flag.** It decomposes
into dependent workstreams. Adding agents to k3s alone does not make Menina
highly available.

Dependency order:

1. **Control-plane state**
   - Replace single-writer SQLite with a replicated/external store (for example
     PostgreSQL) or a deliberately supported HA SQLite topology.
   - Leader election, idempotent job execution, and a single active reconciler.
   - Job snapshots and state tokens must remain consistent across failover.

2. **Multi-node k3s provisioning**
   - `menina deploy` must join server/agent nodes, handle tokens, versions,
     upgrades, and node lifecycle.
   - Scheduling constraints, anti-affinity, PDBs, and drain behavior.

3. **Storage**
   - `local-path` is node-local and cannot fail over.
   - Add replicated/RWX storage (Longhorn, Rook/Ceph, NFS, or a supported
     external class) with explicit per-app opt-in.
   - Stateful apps must never silently move to empty volumes.
   - SQLite remains single-writer; HA implies Postgres or a single pinned writer.

4. **Proxy and routing**
   - Run the proxy on every node or behind an L4 load balancer.
   - Share/version routing snapshots safely across nodes; avoid split-brain.
   - Keep TLS/ACME behavior consistent on every node.

5. **Registry**
   - HA/replicated Zot or an external registry.
   - Shared credential, retention, and pull behavior across nodes.

6. **DR**
   - Automated offsite backups, tested restore, documented RPO/RTO.
   - Restore must rebuild the control plane, config, revisions, registry state,
     and volumes without manual archaeology.

Definition of done for a multi-node MVP:

- One node can be lost without losing the control plane or public routing.
- Stateless apps reschedule automatically; stateful apps survive only when they
  opted into replicated storage.
- Deploys are pinned to a config snapshot and are idempotent across failover.
- Backup/restore is automated and tested, with documented RPO/RTO.
- No silent data loss and no “works only on one node” caveats hidden in docs.

## Tier 4 — supply chain and scale

- Image signing (cosign/Sigstore), scanning (Trivy or similar), and admission
  policy.
- Canary/blue-green, configurable `maxSurge`/`maxUnavailable`, and cross-service
  traffic splitting.
- Log retention/search, alerting, and deploy event history.
- Larger-scale quota, multi-tenancy, and audit export.

## Implementation dependency map

These IDs are for dependency tracking only. Difficulty is end-to-end inside
Menina (validation, defaults, failure semantics, tests), not “an add-on can do
it.” Importance is for multi-service/multi-client production; for a solo
single-node setup most importance scores drop.

| # | Deps | Item | Diff. | Imp. | Why |
| --- | --- | --- | ---: | ---: | --- |
| 1 | 9 | Per-app release identity + state machine | 7 | 9 | Pin digest/config, verification/migration status, previous good release, explicit states. |
| 2 | 1, 3, 4 | Project/env release as partial-failure aggregate | 7 | 8 | Compose per-app outcomes; never claim atomicity. |
| 3 | 1, 4, 9 | Recovery policy (`previous`/`none`/`require-approval`) | 6 | 9 | Triggers, interrupted jobs, previous-good pointer; no data rollback implied. |
| 4 | 1, 9 | Declarative verification (status/body/smoke) | 5 | 9 | Manifest-declared checks + failure policy; reachability is not correctness. |
| 5 | 1, 9 | First-class promotion | 5 | 7 | Digest + reviewed target plan; never rebuild. |
| 6 | 1, 4 | App-aware migration/pre/post hooks | 8 | 9 | Needs a one-off job primitive (not yet indexed), permissions, pinning, timeouts, logs. |
| 7 | — | Optional volume snapshots | 6 | 5 | External CSI capability only; not a backup substitute. |
| 8 | 2, 4, 6 | Minimal dependency ordering | 6 | 7 | Migrations→API→workers; avoid a general DAG. |
| 9 | — | Pin config snapshot on jobs | 5 | 9 | Foundation for release identity and closing the apply/deploy race. |
| 10 | — | Secret staleness annotation/status | 5 | 7 | Hash injected secrets at rollout; should feed release state too. |
| 11 | — | Job watch resume cursor + terminal sentinel | 4 | 5 | Survive SSH drops; propagate remote exit code. |
| 12 | 1, 4 | Webhook operational visibility | 6 | 6 | Versioned payload, delivery status, DLQ/replay; release/verification fields depend on 1/4. |
| 13 | — | Metrics depth + alert rules | 6 | 8 | Deploy duration, queue depth, ready replicas, proxy/registry/disk; external alerting. |
| 14 | — | DR: scheduled backups, RPO/RTO, restore CI | 6 | 10 | Backup exists; automation and proof do not. |
| 15 | — | Finish error taxonomy | 4 | 7 | Stable codes for CAS/stale plans/auth/schema mismatches. |
| 16a | 26, 38b | Project-scoped secret permissions | 7 | 8 | Per-project read/write/rotate; needs the tenant model and enforced scopes. |
| 16b | 26, 38b | Per-project/client registry credentials | 7 | 8 | Replace the shared push credential; prefer short-lived scoped tokens. |
| 17 | 38a, 38b, 46 | Audit capture + query | 5 | 9 | Actor/key/action/target/plan/source; identity, scopes, and the API dispatcher are prerequisites. |
| 18a | 38b, 46 | Authorization model + enforcement | 7 | 8 | General policy beyond secrets/registry; repo metadata is not a boundary. |
| 18b | 18a | Ownership enforcement (`source.repo`) | 4 | 6 | Check the hint; cheap consistency once policy exists. |
| 19a | 18a | SSO/OIDC + short-lived SSH certificates | 7 | 6 | Humans and CI get short-lived certs; HTTP/JWT only when an API lands. |
| 19b | 18a | Per-team quotas | 6 | 6 | Deferred; needs policy and project isolation. |
| 19c | 18a, 25 | NetworkPolicy isolation | 6 | 7 | Deferred; needs project namespaces and policy. |
| 20 | 16a | Secret storage isolation | 8 | 8 | At-rest isolation beyond the shared group; not just API checks. |
| 21 | 16a, 16b, 17, 18a, 18b, 20, 37a, 37b, 37c, 38a, 38b | Multi-client readiness gate | 2 | 9 | Process/validation: no shared admin identity across clients/agents. |
| 22 | 14, 16a, 18a | Per-client backup schedules/retention | 6 | 8 | Fleet policy needs DR automation + project isolation. |
| 23 | 13 | Aggregate fleet overview | 6 | 7 | Failed deploys, health, disk, certs, backups; needs metrics/labels. |
| 24 | 14, 16a, 18a | Replacement-host migration workflow | 8 | 8 | Tested DR + isolation; config/revisions/secrets/registry/volumes. |
| 25 | 16a, 18a | Per-project quotas/limits | 5 | 6 | ResourceQuota/LimitRange + project isolation. |
| 26 | — | Multi-tenant vs one-install-per-client decision | 3 | 8 | Architecture/product choice; affects 16a, 16b, 19a, 19b, 19c, 22, 24, 42, 43, 45. |
| 27 | 1, 9 | HA control-plane state | 9 | 10 | Replace SQLite writer, leader election, idempotent jobs, consistent snapshots. |
| 28 | 27 | Multi-node k3s provisioning | 7 | 9 | Tokens, upgrades, drain, scheduling; HA needs 27. |
| 29 | 28 | Replicated/RWX storage | 9 | 9 | Longhorn/Ceph/NFS; never silently move stateful apps to empty volumes. |
| 30 | 28 | Per-node proxy/routing / L4 LB | 7 | 9 | Shared routing state, no split-brain, consistent TLS/ACME. Edge binary and snapshot subscription landed. |
| 31 | 28 | HA/replicated registry | 8 | 7 | Multi-node pull reliability; per-project credentials land separately. |
| 32 | 14, 24, 27, 28, 29, 30, 31 | Multi-node DR | 9 | 9 | Restore control plane, registry, config, revisions, volumes after node loss. |
| 33 | — | Image signing/scanning/admission | 8 | 7 | Supply-chain trust; can run in parallel. |
| 34 | 2, 3, 4, 5 | Canary/blue-green, maxSurge/maxUnavailable, traffic splitting | 8 | 7 | Needs release sets + verification + recovery + traffic control. |
| 35 | 12, 13 | Log retention/search, alerting, deploy events | 7 | 8 | Needs metrics/events first; much can be external. |
| 36 | 16a, 18a, 19a, 19b, 19c, 25, 42, 43, 45 | Larger-scale quotas/multitenancy/audit export | 9 | 6 | Identity/authorization/quotas must land first. |
| 37a | — | Identity/profile model + storage | 4 | 9 | Name, scopes, keys, expiry, status; the server derives the profile from the key. |
| 37b | 37a | Enrollment token lifecycle | 5 | 9 | One-time, short TTL, hashed at rest, consumed transactionally. |
| 37c | 37a, 37b | Key lifecycle: rotate/revoke/list/TTL | 4 | 8 | Per-key revocation and rotation without disturbing other clients; optional short TTL or SSH certificate issuance. |
| 38a | 37a | Key-derived actor + forced-command wrapper | 6 | 9 | `AuthorizedKeysCommand`/forced command, allowlisted subcommands, server-injected actor. |
| 38b | 38a, 46 | Scope enforcement | 5 | 9 | Check scopes against command, project, environment, and action through the API dispatcher. |
| 39 | 37a, 37c | Client contexts + env/CI credentials | 4 | 7 | Contexts, flag/env precedence, `whoami`, BatchMode, host-key pinning. |
| 40 | 39 | In-process SSH transport | 6 | 5 | Remove `openssh-client` dependency; pin host keys by fingerprint. |
| 41 | 37b, 39 | One-paste bootstrap | 4 | 8 | `setup` prints reachable host, host-key fingerprint, and enrollment command; installer verifies with `whoami`. |
| 42 | 19a | CI workload identity | 5 | 7 | OIDC federation exchanges a workload token for a short-lived client credential; no stored CI key. |
| 43 | 16b, 38b | Registry token service | 6 | 7 | Docker token auth with per-project scopes and short TTL; replaces shared htpasswd. |
| 44 | 18a, 19a, 46 | HTTP API + WebSocket/SSE | 7 | 4 | Optional browser/hosted surface. Not required for multi-tenancy; SSH stays the CLI and break-glass path. HTTP transport landed; auth, SSE/WS, and UI remain. |
| 45 | 18a | Per-tenant rate limits and fairness | 5 | 6 | Bound registry pulls, job throughput, and concurrency so one tenant cannot starve the rest. |
| 46 | — | API command contract and transport parity | 5 | 8 | Dispatcher, versioned request types, actor context, idempotency, shared transport tests. |
| 47 | 46 | API-first desired-state mutations | 5 | 7 | `apply`, `config revisions`, and `config rollback` through the API with server-side revalidation. |
| 49 | — | Edge request metrics and access logs | 5 | 8 | Per-route status/latency/errors, in-flight/bytes, cert expiry, snapshot age, redacted logs. |

Tier 2 is 17 core items, six deferred larger-team controls, and one optional
product-gated interface. The suffixed IDs keep earlier references addressable;
16, 18, 19, 37, and 38 were split, and 46, 47, and 49 cover the remaining API,
desired-state, and edge monitoring work exposed by the refactor. Difficulty totals are
88 for the core, 35 for the deferred controls, and 7 for the optional interface
(130 total). The umbrella items understated identity lifecycle, command
enforcement, and short-lived credential work.

Items 1-15 (Tier 1) landed in the unreleased line, including the previously
hidden one-off Job primitive used by migration hooks and verification smoke
tests. The table remains as a dependency reference for the higher tiers.

The package and role refactor provides the API client and transport boundary,
separate edge and core binaries, and the state package. Items 30 and 44 reflect
the landed transport and edge work. Items 17, 18a, and 38b keep their scores
until item 46 moves the command handlers behind the API dispatcher; the policy,
schema, and query work is unchanged.

## Explicit non-goals for the current line

- Pretending single-node SQLite/local-path is HA.
- Multi-node without replicated storage and a control-plane state store.
- ACME wildcards, HTTP/3/QUIC, built-in WAF, or a full Kubernetes distribution.
- Multi-team production positioning before Tier 2 and Tier 3 land.

## How priorities are chosen

1. Correctness and data safety first (races, silent loss, stale state).
2. Agent determinism and machine-readable contracts.
3. Operator visibility and recovery.
4. Team/security controls.
5. Multi-node/HA, then scale and supply chain.

If a feature cannot be verified, rolled back, and explained in the CLI, it is
not ready for this roadmap.
