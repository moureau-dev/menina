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
- Versioned API command dispatcher with actor context, in-memory idempotency,
  shared transport parity tests, and a stdio adapter.
- API-first desired-state mutations: `apply`, `config revisions`, and
  `config rollback` execute through the API with server-side base-state and
  file-hash revalidation, with local fallback when the control plane is down.
- Identity profile model and storage: profiles, scopes, attached keys, expiry,
  status, last seen, and server-side key resolution.
- Enrollment token lifecycle: one-time tokens, short TTL, hashed at rest, and
  transactional consumption that attaches a key to a profile.
- Key lifecycle: list, fingerprint, revoke, rotate, and optional short TTL.
  SSH certificate issuance remains under the SSO/OIDC work.
- Key-derived actor and forced-command wrapper: authenticated keys resolve to
  profiles, commands are allowlisted, the actor is injected server-side, and
  identity administration covers profiles, keys, and enrollment tokens.
- Scope enforcement for API commands and forced commands, including
  project-scoped and environment-scoped grants.
- Audit capture and query for API and forced-command actions, including actor,
  key fingerprint, target, project/environment/app, outcome, and timestamps.
- Authorization policy with global, project, environment, and app-level scopes,
  enforced centrally by the API authorizer and forced-command wrapper.
- Ownership enforcement: `source.repo` is checked server-side for apply and
  prune plans, not only in the client.
- Client contexts: stored targets, `--profile` / `MENINA_PROFILE` precedence,
  pinned SSH host keys, and `whoami`.
- One-paste bootstrap: `setup` prints the reachable host and host-key
  fingerprint, `identity bootstrap` creates a profile/token, and `enroll`
  consumes the token and verifies with `whoami`.
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

The work is split into independently shippable pieces. The API command path
(item 46) and API-first desired-state mutations (item 47) are landed, and scope
enforcement (item 38b), audit, and authorization are in place, so item 44 can
attach to them. The multi-client readiness gate is the remaining validation
step.

### Client identity and access

1. **40 In-process SSH transport.** Remove the `openssh-client` dependency and
    pin host keys by fingerprint.

### Secrets and registry

2. **16a Project-scoped secret permissions.**
3. **20 Secret storage isolation** beyond the shared `menina` group.
4. **16b Per-project/client registry credentials**, preferring short-lived
    registry tokens over a shared htpasswd.

### Audit and authorization

5. **21 Multi-client readiness gate.**

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
| 16a | 26 | Project-scoped secret permissions | 7 | 8 | Per-project read/write/rotate; needs the tenant model and enforced scopes. |
| 16b | 26 | Per-project/client registry credentials | 7 | 8 | Replace the shared push credential; prefer short-lived scoped tokens. |
| 19a | — | SSO/OIDC + short-lived SSH certificates | 7 | 6 | Humans and CI get short-lived certs; HTTP/JWT only when an API lands. |
| 19b | — | Per-team quotas | 6 | 6 | Deferred; needs policy and project isolation. |
| 19c | 25 | NetworkPolicy isolation | 6 | 7 | Deferred; needs project namespaces and policy. |
| 20 | 16a | Secret storage isolation | 8 | 8 | At-rest isolation beyond the shared group; not just API checks. |
| 21 | 16a, 16b, 20 | Multi-client readiness gate | 2 | 9 | Process/validation: no shared admin identity across clients/agents. |
| 22 | 14, 16a | Per-client backup schedules/retention | 6 | 8 | Fleet policy needs DR automation + project isolation. |
| 23 | 13 | Aggregate fleet overview | 6 | 7 | Failed deploys, health, disk, certs, backups; needs metrics/labels. |
| 24 | 14, 16a | Replacement-host migration workflow | 8 | 8 | Tested DR + isolation; config/revisions/secrets/registry/volumes. |
| 25 | 16a | Per-project quotas/limits | 5 | 6 | ResourceQuota/LimitRange + project isolation. |
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
| 36 | 16a, 19a, 19b, 19c, 25, 42, 43, 45 | Larger-scale quotas/multitenancy/audit export | 9 | 6 | Identity/authorization/quotas must land first. |
| 40 | — | In-process SSH transport | 6 | 5 | Remove `openssh-client` dependency; pin host keys by fingerprint. |
| 42 | 19a | CI workload identity | 5 | 7 | OIDC federation exchanges a workload token for a short-lived client credential; no stored CI key. |
| 43 | 16b | Registry token service | 6 | 7 | Docker token auth with per-project scopes and short TTL; replaces shared htpasswd. |
| 44 | 19a | HTTP API + WebSocket/SSE | 7 | 4 | Optional browser/hosted surface. Not required for multi-tenancy; SSH stays the CLI and break-glass path. HTTP transport landed; auth, SSE/WS, and UI remain. |
| 45 | — | Per-tenant rate limits and fairness | 5 | 6 | Bound registry pulls, job throughput, and concurrency so one tenant cannot starve the rest. |
| 49 | — | Edge request metrics and access logs | 5 | 8 | Per-route status/latency/errors, in-flight/bytes, cert expiry, snapshot age, redacted logs. |

Tier 2 is 5 core items, six deferred larger-team controls, and one optional
product-gated interface. The suffixed IDs keep earlier references addressable;
16, 18, 19, 37, and 38 were split; 49 covers the remaining edge monitoring
work exposed by the refactor. Difficulty totals are 30 for the core, 35 for the
deferred controls, and 7 for the optional interface (72 total). The umbrella
items understated identity lifecycle, command enforcement, and short-lived
credential work.

Items 1-15 (Tier 1) landed in the unreleased line, including the previously
hidden one-off Job primitive used by migration hooks and verification smoke
tests. The table remains as a dependency reference for the higher tiers.

The package and role refactor provides the API client and transport boundary,
separate edge and core binaries, and the state package. Items 30 and 44 reflect
the landed transport and edge work. The API dispatcher, key-derived actor path,
scope enforcement, audit, ownership, and authorization policy are in place; the
multi-client readiness gate remains.

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
