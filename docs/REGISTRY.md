# Menina registry

Menina bundles a private OCI image registry — **[Zot](https://zotregistry.dev)**
— so the platform is self-contained: you push images to your own host and Menina
deploys them, with no external registry required. A push can **trigger the
deploy automatically**.

The registry is a normal Kubernetes workload managed by Menina (see
[KUBERNETES.md](KUBERNETES.md)); its public domain is routed through the
[proxy](PROXY.md) like any app.

## What Menina deploys

When `registry.enabled` (default), `menina deploy` reconciles Zot in the
`menina-registry` namespace (`registry.namespace`):

| Object | Name | Notes |
| --- | --- | --- |
| Deployment | `zot` | `registry.zotImage:registry.zotVersion` (pinned, default `v2.1.22`), 1 replica, port 5000 |
| Service | `zot` | ClusterIP, port 5000 (what the proxy routes to) |
| PVC | `zot-data` | `registry.pvcSize` (default `10Gi`), `registry.storageClass` (k3s `local-path` by default) |
| ConfigMap | `zot-config` | generated `config.json` (see below) |
| Secret | `zot-htpasswd` | bcrypt htpasswd, only when `registry.auth.enabled` |
| Route | `registry.domain` | via the proxy; the registry gets TLS like any host |

Deploy waits for Zot's rollout (up to 5 min) and fails loudly if it doesn't
become ready. The Zot version is validated: **≥ v2.1.3** is required (the
CloudEvents events extension), targeting the Zot v2 config schema.

## Zot configuration (generated)

Menina builds `config.json` from `internal/registry/zot.go`:

- **Storage**: `dedupe: true`, `gc: true`, root directory `/var/lib/registry`.
- **HTTP**: `0.0.0.0:5000`, htpasswd auth, docker-compat (`docker2s2`).
- **Retention** (when enabled): keep the most recently pushed
  `registry.retention.keepRecentTags` tags per repository (`**`), and
  **never delete untagged manifests** — a rollback may pin a digest whose tag
  moved, and GC must keep it.
- **Events**: the Zot `events` extension posts CloudEvents to Menina's control
  plane (below).

## Authentication

- Push credentials live in a secret file (mode `0600`), referenced from
  `config.yml` as `registry.auth.passwordSecretRef: file:...`. The password is
  bcrypt-hashed into the `zot-htpasswd` Secret — Zot never sees the plaintext.
- `menina registry credential` prints the current username/password (for
  `docker login`).
- `menina registry credential --regenerate` generates a new random password,
  stores it, updates `config.yml`, and **applies it everywhere**: the Zot
  htpasswd Secret, the k3s containerd `registries.yaml`, then restarts k3s so
  pulls use the new credential.
- Pulls from k3s work because `menina deploy` writes the credential into k3s's
  `registries.yaml` (mode `0600`). The token/credential is never baked into app
  manifests; pods pull via containerd.

## Pushing images

```bash
menina registry credential                 # username + password
docker login registry.example.com -u <user>
docker build -t registry.example.com/my-app/api:production .
docker push registry.example.com/my-app/api:production
```

Repository naming is free-form, but a common convention is
`<registry.domain>/<project>/<app>:<tag>`, matching the app's `image.repository`
and `image.tag`. Menina matches pushes to apps by repository **and** tag
(below).

## Automatic deploy on push

1. On push, Zot's events extension POSTs a CloudEvent (`...image.updated`) to
   Menina's registry listener at `http://<nodeIP>:9000/v2/events`
   (`registry.notifications.path`), authenticated with a **Bearer token**
   (`registry.notifications.tokenRef`). The endpoint address is auto-derived
   from the node's internal IP; override with
   `registry.notifications.endpoint` if needed.
2. `menina serve` verifies the token (constant-time) and parses the payload —
   Menina understands both Zot's **CloudEvents** (binary or structured) and the
   OCI distribution `events` array for compatibility
   (`internal/registry/events.go`).
3. For each push event, Menina finds every **enabled** app whose
   `image.repository` matches the pushed repository and whose `image.tag`
   matches the pushed tag, and enqueues a **deploy job**. Matching is tolerant
   of a leading `registry.domain/` on the configured repository.
4. Jobs are **deduplicated** by `(registry, repository, digest, deliveryID)`, so
   redelivered notifications don't redeploy. Enqueuing **supersedes** earlier
   pending jobs for the same app — a burst of pushes converges to the latest.
5. A pool of workers (`platform.workers`, default 4) claims jobs, resolves the
   image to a **digest**, and reconciles the Deployment. If the app was deleted
   or disabled, the job is dropped rather than retried forever.

Disabled apps and apps without exposure are skipped safely; a push never fails
the pipeline if no app matches.

## Digest pinning

Before deploying, Menina resolves the tag to a digest
(`HEAD /v2/<repo>/manifests/<tag>` → `Docker-Content-Digest`) and pins the
Deployment to `repository@sha256:…` (`internal/registry/digests.go`). This makes
deploys immutable and makes rollback deterministic:
`menina app rollback` restores a recorded digest even if the tag has since
moved. If the registry omits the header, Menina falls back to hashing the
manifest body.

## Retention and garbage collection

With `registry.retention.enabled`:
- Keep the `keepRecentTags` most recently pushed tags per repository.
- Deduplication is on, and GC runs.
- Untagged manifests are **never** deleted (protects rollback targets).

## Storage and backups

Image data lives in the `zot-data` PVC on the cluster's `local-path` provisioner
(`registry.storageClass`). `menina backup` backs up the **SQLite database**
(release history, routes, jobs), **not** image blobs — for image durability, back
up/snapshot the node's local-path volume.

## Using an external registry

Set `registry.enabled: false` and configure each app's
`image.repository` to an external registry (add pull secrets if needed). Menina
then only consumes images; the **push-event auto-deploy** depends on Zot's
events extension, so with an external registry use `menina app deploy` or CI
instead.

> Note: Zot's **registry notifications** (Zot → Menina, this doc) are distinct
> from Menina's outbound **deploy notifications** (`webhooks.deploy`,
> `menina notify …`), which POST deploy outcomes to Slack/Discord/etc.

## CLI

| Command | Purpose |
| --- | --- |
| `menina registry status` | Zot health (`/v2/`) |
| `menina registry credential` | Print push username/password |
| `menina registry credential --regenerate` | Rotate and apply a new password |
| `menina deploy` | (Re)deploy Zot, config, secret, route and k3s credentials |
| `menina doctor` | Diagnose registry reachability alongside other checks |

## Configuration reference

```yaml
registry:
  enabled: true
  domain: registry.example.com
  zotImage: ghcr.io/project-zot/zot
  zotVersion: v2.1.22         # must be >= v2.1.3
  namespace: menina-registry
  pvcSize: 10Gi
  storageClass: ""            # "" = cluster default (local-path)
  auth:
    enabled: true
    username: menina
    passwordSecretRef: file:/etc/menina/secrets/registry-password
  retention:
    enabled: true
    keepRecentTags: 3
  notifications:
    enabled: true
    path: /v2/events
    endpoint: ""              # auto: http://<nodeIP>:9000/v2/events
    tokenRef: file:/etc/menina/secrets/notifications-token
```

## Failure modes

- **Zot not ready**: `menina deploy` reports the failed rollout; `registry
  status` fails; apps can't pull.
- **Notifications not configured**: the `/v2/events` endpoint returns `503`;
  pushes still store images, they just don't auto-deploy.
- **Bad/rotated token**: events get `401` and are ignored (no deploy); re-run
  `menina deploy`.
- **k3s can't pull**: check `registries.yaml` (mode `0600`) and that k3s was
  restarted after a credential change.
- **Unsupported Zot version**: deploy is rejected before touching the cluster.

## Verifying on a real cluster

- `scripts/push-deploy-e2e.sh` — push → Zot event → auto-deploy → rollout.
- `scripts/registry-pull-e2e.sh` — k3s pulls from the in-cluster registry.
