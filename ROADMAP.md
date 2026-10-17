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

## Landed recently (through v0.0.13)

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

## Tier 1 — finish determinism and operations (next)

- Pin the resolved config snapshot on deployment jobs so a deploy cannot execute
  against config written after it was queued. Surface the pinned version in
  `job watch`, `app status`, and notifications.
- Secret staleness: annotate Deployments with the hash of injected secrets and
  report “secret rotated, rollout stale” in `app status`/`secret list`.
- Job watch resilience: resume cursor/last-event ID and a terminal sentinel
  carrying the remote exit code; clean cancellation across SSH.
- Webhook operations: versioned payload contract, last-delivery status in CLI
  and JSON, dead-letter/replay, and idempotency verification.
- Metrics depth: deploy duration/success, queue depth, per-app ready replicas,
  proxy health, registry storage, node disk. Document labels and a reference
  Prometheus/Alertmanager rule set.
- DR: scheduled/offsite backup guidance, documented RPO/RTO, restore test in CI.
- Finish error taxonomy: CAS conflicts, stale plans, auth failures, and schema
  mismatches should all use stable codes/exit values.

Exit criteria: no known apply/deploy race; stale secrets are visible; a dropped
SSH session does not lose job state; a failed deploy is observable without
manual checks.

## Tier 2 — team and security

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

## Tier 3 — multi-node and HA/DR

**Multi-node is a huge win, but it is a program, not a flag.** It is listed in
the HA/DR tier because it decomposes into dependent workstreams. Adding agents
to k3s alone does not make Menina highly available.

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
- Canary/blue-green, configurable `maxSurge`/`maxUnavailable`, migration jobs,
  dependency ordering, automatic rollback on failed verification.
- Log retention/search, alerting, and deploy event history.
- Larger-scale quota, multi-tenancy, and audit export.

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
