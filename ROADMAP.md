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

## Landed recently (through v0.0.14)

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

Guiding principle: **make failure paths as deliberate and predictable as the
happy path.** A release must be identifiable, verifiable, recoverable, and
honest about what was and was not rolled back.

## Tier 1 — release model, determinism, and operations (landed)

Tier 1 landed in the unreleased line after v0.0.14. It is recorded here as the
baseline the higher tiers build on, not as remaining work.

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

## Tier 2 — team and security

**Shared-access gate:** before multiple independent clients or autonomous agents
share one host, require project-scoped credentials and attributable audit logs.
A shared administrative identity is not a tenant boundary. RBAC/SSO can follow.

Ordered cheapest-first:

1. Per-project secret permissions and per-user registry credentials.
2. Operator audit log: who applied/rotated/deleted what, with actor, plan/state
   token, source repo, and timestamps.
3. `source.repo` is an ownership hint, not a security boundary; enforce real
   authorization separately.
4. RBAC/SSO, per-team quotas, and network policies.
5. Secret isolation beyond the shared `menina` group.

Exit criteria: multiple operators can share a host without sharing credentials
or silently overwriting each other; actions are attributable and reviewable.
Independent clients/agents do not share an administrative identity.

## Tier 3 — fleet operations, multi-node, and HA/DR

### Fleet operations

These are useful even while staying single-node:

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
| 16 | 26 | Per-project secret permissions + per-user registry creds | 8 | 9 | Tenant model affects scoping; isolation/revocation required. |
| 17 | 16 | Operator audit log | 6 | 9 | Actor/source/plan/tokens; weak without identity but useful early. |
| 18 | 16 | Real authorization vs `source.repo` hint | 7 | 8 | Enforce ownership; repo metadata is not a boundary. |
| 19 | 16, 17, 18 | RBAC/SSO, team quotas, NetworkPolicy | 9 | 8 | Large identity/infra program on top of creds/audit/authorization. |
| 20 | 16 | Secret isolation beyond shared `menina` group | 8 | 9 | Group-readable secrets are not tenant isolation. |
| 21 | 16, 17 | Shared-access gate | 2 | 9 | Process/validation: no shared admin identity across clients/agents. |
| 22 | 14, 16 | Per-client backup schedules/retention | 6 | 8 | Fleet policy needs DR automation + project isolation. |
| 23 | 13 | Aggregate fleet overview | 6 | 7 | Failed deploys, health, disk, certs, backups; needs metrics/labels. |
| 24 | 14, 16 | Replacement-host migration workflow | 8 | 8 | Tested DR + isolation; config/revisions/secrets/registry/volumes. |
| 25 | 16 | Per-project quotas/limits | 5 | 6 | ResourceQuota/LimitRange + project isolation. |
| 26 | — | Multi-tenant vs one-install-per-client decision | 3 | 8 | Architecture/product choice; affects 16, 19, 22, 24. |
| 27 | 1, 9 | HA control-plane state | 9 | 10 | Replace SQLite writer, leader election, idempotent jobs, consistent snapshots. |
| 28 | 27 | Multi-node k3s provisioning | 7 | 9 | Tokens, upgrades, drain, scheduling; HA needs 27. |
| 29 | 28 | Replicated/RWX storage | 9 | 9 | Longhorn/Ceph/NFS; never silently move stateful apps to empty volumes. |
| 30 | 28 | Per-node proxy/routing / L4 LB | 8 | 9 | Shared routing state, no split-brain, consistent TLS/ACME. |
| 31 | 28 | HA/replicated registry | 8 | 7 | Multi-node pull reliability; shared credential remains. |
| 32 | 14, 24, 27, 28, 29, 30, 31 | Multi-node DR | 9 | 9 | Restore control plane, registry, config, revisions, volumes after node loss. |
| 33 | — | Image signing/scanning/admission | 8 | 7 | Supply-chain trust; can run in parallel. |
| 34 | 2, 3, 4, 5 | Canary/blue-green, maxSurge/maxUnavailable, traffic splitting | 8 | 7 | Needs release sets + verification + recovery + traffic control. |
| 35 | 12, 13 | Log retention/search, alerting, deploy events | 7 | 8 | Needs metrics/events first; much can be external. |
| 36 | 16, 18, 19, 25 | Larger-scale quotas/multitenancy/audit export | 9 | 6 | Identity/authorization/quotas must land first. |

Items 1-15 (Tier 1) landed in the unreleased line, including the previously
hidden one-off Job primitive used by migration hooks and verification smoke
tests. The table remains as a dependency reference for the higher tiers.

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
