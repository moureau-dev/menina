# Menina on Kubernetes (k3s)

Menina is the control plane; **k3s is the scheduler and runtime**. Menina owns
config, builds routing, and reconciles desired state into Kubernetes objects.
Instead of shipping manifests by hand, you declare apps/environments and Menina
creates the `Deployment`, `Service`, `PersistentVolumeClaim` and (optionally)
`HorizontalPodAutoscaler` that run them.

On the supported setup, k3s and Menina run on the **same single node**:
`menina proxy` owns `:80`/`:443` on the host, and forwards to Kubernetes
ClusterIPs (see [PROXY.md](PROXY.md) for the edge side).

## Provisioning (k3s install)

`menina deploy` (via `menina setup`) installs k3s with the upstream script
(`https://get.k3s.io`) only if it isn't already present — an existing k3s is
**left untouched**:

```
INSTALL_K3S_EXEC="server --disable traefik --disable servicelb"
```

- **Traefik is disabled** because Menina's proxy is the edge; running Traefik
  too would fight for `:80`/`:443`.
- **ServiceLB (Klipper) is disabled** for the same reason — Menina doesn't use
  `LoadBalancer` Services; it reaches ClusterIPs directly.

Menina also writes k3s's `registries.yaml` (credentials for the local Zot
registry, mode `0600`) and restarts k3s so containerd picks up new/rotated
credentials. See `internal/infrastructure/k3s.go`.

> Managed vs unmanaged: `kubernetes.managed: true` (default) means Menina
> installs/refreshes k3s. With an external cluster, set `managed: false` and
> provide `kubernetes.kubeconfig`.

## Talking to the API

Menina speaks to the Kubernetes API with its **own small REST client**
(`internal/kube`) — no `client-go`, no `kubectl` dependency at runtime. It loads
`kubernetes.kubeconfig` (or the scoped kubeconfig it generates) and talks to the
API server directly; pod logs and the routing snapshot stream over the same
path.

## RBAC: a scoped identity, never admin

`menina deploy` provisions a **least-privilege** identity and hands the control
plane *that* kubeconfig — Menina never runs as cluster-admin:

- ClusterRole **`menina-platform`** with only (see
  `internal/deployment/access.go`):
  - `namespaces`: get/list/create
  - `nodes`: get/list
  - `services`: get/list/watch/create/update/delete
  - `configmaps`, `secrets`, `persistentvolumeclaims`: get/list/watch/create/update
  - `pods`: get/list/watch; `pods/log`: get
  - `apps/deployments`: get/list/watch/create/update/delete
  - `autoscaling/horizontalpodautoscalers`: get/list/watch/create/update/delete
- **ServiceAccount `menina`** in `menina-system`, bound via ClusterRoleBinding
  `menina-platform`.
- A long-lived token Secret (`menina-token`) is created and its token + CA are
  used to build the kubeconfig the platform actually uses.

The role is **reconciled on every deploy** (rules updated in place), so upgrading
Menina updates permissions without manual `kubectl`. The real-k3s RBAC is
verified by `scripts/rbac-e2e.sh`.

## Namespaces

| Namespace | Purpose |
| --- | --- |
| `menina-system` | The platform identity (ServiceAccount, RBAC, token). |
| `menina-registry` | The Zot image registry (`registry.namespace`, default). |
| `<app-namespace>` | Application workloads. Defaults to `platform.defaults.namespace` (`menina-apps`). |

**Environments are isolated by namespace.** A project's base namespace comes from
`project.yml → namespace` (else the default). Each environment then gets
`<base>-<environment>` unless its `environments/<env>.yml → namespace` overrides
it — so `production` and `staging` never share a namespace (deleting one can't
affect the other). Namespaces Menina creates are labelled
`app.kubernetes.io/managed-by: menina`.

## What runs per app

For each enabled app in each environment, Menina reconciles:

- **Deployment** named after the app (`app.kubernetes.io/name: <app>`,
  `managed-by: menina`, plus `menina.io/{project,environment,app}` labels).
  - Image pinned by **digest** when known (`repo@sha256:…`), else `repo:tag`;
    `imagePullPolicy` from config (default `IfNotPresent`).
  - Env vars sorted for stable diffs; CPU/memory set as **both requests and
    limits** (equal) when configured.
  - Readiness + liveness HTTP probes when a `health` path is set (same port).
  - Volumes: a detached data **PVC** (`<app>-data`, mounted at `storage.mountPath`
    with `fsGroup: 1000`) when storage is enabled; optional ConfigMap and Secret
    mounts.
  - `worker` workloads run a container with no Service/port; `service` workloads
    get a port and a Service.
- **Service** (service apps with a port): `ClusterIP`, `port`/`targetPort` =
  `workload.port`, selector `app.kubernetes.io/name: <app>`. This ClusterIP is
  what the proxy routes to.
- **HorizontalPodAutoscaler** (`autoscaling/v2`) named after the app when the
  replica spec is a range (`max > min`), targeting CPU utilization. When
  replicas are fixed, any stale HPA is **deleted** so the Deployment's replica
  count is authoritative. See the [CLI reference](CLI.md#environments--scaling)
  for baseline vs per-environment scaling.
- **PersistentVolumeClaim** `<app>-data` when storage is enabled (default
  `1Gi`, `ReadWriteOnce`, optional `storageClass`; k3s's `local-path`
  provisioner by default).

Applying is **idempotent**: an unchanged pod template does not trigger a
rollout. Updates replace `spec`/`labels` in place, preserving server-assigned
fields (e.g. `clusterIP`, `ipFamilies`). "Restart" bumps a
`menina.io/restartedAt` annotation.

## Reconciliation loop

`menina serve` runs a reconciler that:

1. Resolves all projects/environments/apps from config.
2. Applies the desired Deployment/Service/HPA/PVC for each app.
3. **Prunes** Menina-managed workloads, Services and HPAs that are no longer in
   the desired state (identified by the `managed-by: menina` label) — deletes
   are config-driven, so removing an app/environment cleans up its resources.
4. Builds the **routing snapshot** from exposed apps' Service ClusterIPs and
   publishes it to the proxy (see [PROXY.md](PROXY.md)).

Config changes are picked up by a file watcher; `menina deploy` and
`menina app deploy` trigger reconciliation explicitly. PVCs and release history
are **retained** on delete (deleting a project/environment does not delete data).

## The registry (Zot) in-cluster

When `registry.enabled`, Menina runs **Zot** as a workload in `menina-registry`
(see the [registry reference](REGISTRY.md) for auth, retention, and
push-triggered deploys):

- A Deployment + ClusterIP Service + **PVC** (`registry.pvcSize`, default
  `10Gi`) holding images at the local-path storage.
- A ConfigMap with the generated Zot config (dedupe + GC on) and an htpasswd
  **Secret** for auth.
- Retention: keep the most-recent `registry.retention.keepRecentTags` tags per
  repo, and **never delete untagged manifests** (a rollback may pin a digest
  whose tag moved).
- **Push events**: Zot posts CloudEvents to the control plane's `:9000`
  events endpoint (token-authenticated). That is what makes
  [automatic deploy on push](CLI.md) work. Requires Zot ≥ v2.1.3 (validated).
- The registry's public domain (`registry.domain`) is routed through the proxy
  like any app.

## Node networking to ClusterIPs

The host proxy reaches `ClusterIP:port` because kube-proxy programs DNAT rules
in the host/node network namespace. Two implications:

- **Test reachability with TCP, not ICMP** — `ping <ClusterIP>` fails even when
  routing works; `menina doctor` does a TCP check per route.
- The ClusterIP is stable across pod replacement, so routes don't change during
  rollouts (a brief no-ready-endpoints window yields `502`, then recovers).

## Storage on the host

k3s uses its bundled `local-path` provisioner for PVCs (backed by the node's
disk). Menina's own host paths (`host.dataDir`, default `/var/lib/menina`:
`registry/`, `releases/`, `backups/`, `certs/`) hold the SQLite database,
release metadata, backups and TLS material — not cluster state. Cluster state is
k3s's SQLite in its data dir; application data lives in PVCs.

## Operating it

- `menina deploy` — reconcile infrastructure (k3s + registry) and platform
  services; `menina deploy infra|platform` to scope it.
- `menina status`, `menina routes`, `menina doctor` — inspect health, routing and
  connectivity (see the [CLI reference](CLI.md)).
- `menina serve` owns reconciliation and the routing snapshot; `menina proxy`
  serves traffic. Both are systemd units installed by `deploy`; the proxy runs in
  the host network namespace.
- Menina never mutates k3s beyond install/restart and its own namespaces/objects;
  it does not touch Traefik (disabled) or unrelated workloads.

## Failure modes

- **k3s API unavailable**: `report`/`doctor` surface it; reconciliation retries;
  the proxy keeps serving its last good routing snapshot.
- **Service has no ClusterIP** (`ClusterIP: None` or missing): that route is
  dropped and logged; other routes are unaffected.
- **Stale resources**: anything labelled `managed-by: menina` that leaves the
  desired state is pruned.
- **RBAC drift**: re-running `menina deploy` restores the `menina-platform` role
  to the expected rules.

## Verifying on a real cluster

Scripts that exercise this end-to-end:

- `scripts/rbac-e2e.sh` — the scoped role can create Deployments/Services/HPAs.
- `scripts/push-deploy-e2e.sh` — push → Zot event → auto-deploy → rollout.
- `scripts/registry-pull-e2e.sh` — k3s pulls from the in-cluster registry.
- `scripts/k3s-net-e2e.sh` — host → ClusterIP reachability over TCP.
