# Operations

Privileges, security posture, backups, upgrades, and running Menina for
development.

See also: [CONFIGURATION.md](CONFIGURATION.md), [KUBERNETES.md](KUBERNETES.md),
[PROXY.md](PROXY.md), [REGISTRY.md](REGISTRY.md), [CLI.md](CLI.md).

## Privileges & users

Only `menina install`, `menina setup` and `menina deploy` need root — they create
the `menina` system user, install k3s, deploy Zot, write systemd units and bind
`:80`/`:443`. The long-running `menina serve` / `menina proxy` services run as
the unprivileged `menina` user.

Day-to-day commands run as **any user in the `menina` group** (the installer
adds `$SUDO_USER` automatically; otherwise `sudo usermod -aG menina $USER`, then
log out and back in). The config directory is group-writable
(`root:menina`, setgid) and the control socket is group-accessible, so project/
app management, `status`, `routes`, `logs`, `doctor`, `app deploy`,
`app rollback` and `registry credential` need no sudo.

## Security & recovery

- `menina serve` and `menina proxy` run as a dedicated unprivileged user with
  systemd hardening (`NoNewPrivileges`, `ProtectSystem=full`, `ProtectHome`,
  `ProtectKernel*`, `ProtectControlGroups`, `PrivateDevices`, `PrivateTmp`,
  `RestrictNamespaces`, `RestrictSUIDSGID`, `LockPersonality`, and a minimal
  `RestrictAddressFamilies`). Only the proxy gets `CAP_NET_BIND_SERVICE`.
- The registry webhook is bearer-token authenticated and **fails closed**: if
  notifications are enabled without a valid token, `menina serve` refuses to
  start; if disabled, no listener starts. Forwarded headers are trusted only
  from configured proxies; untrusted `X-Forwarded-Host`/`Forwarded` are
  stripped. The proxy routes only configured hostnames (unknown hosts get 404)
  and never dials a client-controlled destination.
- **Kubernetes access is a scoped identity.** `menina deploy` creates a
  ServiceAccount (`menina-system/menina`), a `menina-platform` ClusterRole with
  only the namespaces/nodes(read)/services/configmaps/secrets/PVCs/pods/pods-log/
  deployments/hpa verbs it needs, and a long-lived token; the service kubeconfig
  uses that token only. Menina never falls back to the admin kubeconfig as the
  service credential. See [KUBERNETES.md](KUBERNETES.md#rbac-a-scoped-identity-never-admin)
  and `scripts/rbac-e2e.sh`.
- `menina deploy` propagates systemd install/enable/start failures (nonzero
  exit, no success message); `menina app deploy` waits for the rollout and
  reports succeeded/failed; Zot readiness is verified before deploy reports
  success.
- CI registry credentials are separate from cluster credentials.
- On SIGTERM the control plane stops accepting work, re-queues in-progress
  deployments and flushes state. When Kubernetes is unavailable it stays up,
  keeps queuing events and retries with backoff. After a database restore,
  Menina reconciles against the live cluster and registry rather than trusting
  the restored database blindly.

## Backups and migration

`menina backup` creates a **portable `tar.gz` bundle** and `menina restore`
applies it — this is how you migrate to another host:

```bash
menina backup --out /root/menina.tgz                       # DB + config.yml + projects/
sudo menina backup --out /root/full.tgz --with-volumes --with-images
MENINA_BACKUP_PASSPHRASE=... sudo menina backup --out /root/enc.tgz \
  --with-secrets --with-volumes --with-images
```

- **Always**: a consistent `VACUUM INTO` snapshot of the SQLite database,
  `config.yml` + `projects/`, TLS material, and `manifest.json` (version,
  timestamp, components, checksums).
- `--with-secrets`: include `secrets/` (registry password, webhook/CF tokens).
  Combine with `--passphrase` (or `MENINA_BACKUP_PASSPHRASE`) to **encrypt** the
  archive (scrypt + AES-GCM); without it secrets are stored in plaintext.
- `--with-volumes`: application volume data (each app's `<app>-data` PVC).
- `--with-images`: the registry's `zot-data` PVC (the image blobs).
- Volume/image capture reads the k3s `local-path` storage directly (fast), so
  `--with-volumes`/`--with-images` **require root**. By default it is
  **crash-consistent** (copied while the workload runs); add `--consistent` to
  scale the owning workload (or Zot) to 0 for the copy and back afterwards.
- Archive format is `tar.gz` (preserves ownership/permissions), not zip.

Restore **stops nothing by itself** — stop the writer first:

```bash
sudo systemctl stop menina-serve
sudo menina restore /root/full.tgz --force --with-volumes --with-images
sudo systemctl start menina-serve menina-proxy   # migrations run on start
```

`--force` overwrites the existing database/config. Without `--with-volumes`/
`--with-images`, only DB + config (+ secrets/certs if present) are applied.
After a restore, Menina reconciles against the live cluster and registry rather
than trusting the restored database blindly.

See [REGISTRY.md](REGISTRY.md#storage-and-backups) for the registry's storage
layout.

### Scheduled backups and offsite copies

`menina deploy platform` installs `menina-backup.service` and a daily
`menina-backup.timer` (03:17 with up to 30 minutes of jitter). The unit writes a
timestamped archive under the data directory's backup path. It does not include
secrets, volumes, or registry images by default; adjust the unit if you accept
those costs:

```bash
systemctl list-timers menina-backup.timer
journalctl -u menina-backup.service
```

Copy the resulting archive off the host (object storage, another server, or a
backup service) and encrypt it when it contains secrets. A backup that only
exists on the host it protects is not a disaster-recovery plan.

The timer is only a schedule. Menina cannot promise a recovery point until you
choose an **RPO** (how much data you can lose) and an **RTO** (how long a
restore may take). A practical single-node baseline:

| Target | Baseline |
| --- | --- |
| RPO | 24 hours for config/database, plus the last offsite volume snapshot |
| RTO | 1-2 hours for a prepared replacement host with the same domain and credentials |

Change the timer interval to tighten the RPO. Volumes and registry images need
`--with-volumes`/`--with-images` (root, and ideally `--consistent`) or a
storage-level snapshot; the database/config archive alone does not restore
application data.

### Restore test

The backup round trip (`TestCreateRestoreRoundTrip`) runs in CI, but also test
the full host path on a schedule:

```bash
sudo menina backup --out /root/drill.tgz --with-secrets --with-volumes --with-images
# on a scratch host
sudo systemctl stop menina-serve
sudo menina restore /root/drill.tgz --force --with-volumes --with-images
sudo systemctl start menina-serve menina-proxy
menina status && menina release status --project <p> --environment production
```

Record the date, archive size, restore duration, and any manual steps. If a step
is manual, write it down or automate it before calling the DR plan tested.

## Metrics and alerts

`menina serve` exposes Prometheus text metrics on its event listener
(`/metrics`, default `:9000`). The listener runs even when registry
notifications are disabled; in that case `/v2/events` returns 503.

- deploy health: `menina_releases{state}`, `menina_deploy_success_total`,
  `menina_deploy_failure_total`, `menina_deploy_duration_seconds_sum/count`
- queue: `menina_jobs{status}`, `menina_queue_depth`
- apps: `menina_app_ready_replicas`, `menina_app_desired_replicas`
- edge/control plane: `menina_up`, `menina_proxy_snapshot_ready`,
  `menina_routes`, `menina_snapshot_version`
- node/registry storage: `menina_node_disk_total_bytes`,
  `menina_node_disk_available_bytes` (the bundled registry shares this
  filesystem)

Reference rules (tune thresholds to the host):

```yaml
groups:
  - name: menina
    rules:
      - alert: MeninaDeployFailures
        expr: increase(menina_deploy_failure_total[30m]) > 0
        labels: { severity: warning }
        annotations: { summary: "A Menina release failed in the last 30m" }
      - alert: MeninaQueueBacklog
        expr: menina_queue_depth > 10
        for: 15m
        labels: { severity: warning }
        annotations: { summary: "Deployment queue is backing up" }
      - alert: MeninaAppNotReady
        expr: menina_app_ready_replicas < menina_app_desired_replicas
        for: 10m
        labels: { severity: warning }
        annotations: { summary: "An app has fewer ready replicas than desired" }
      - alert: MeninaDiskLow
        expr: menina_node_disk_available_bytes / menina_node_disk_total_bytes < 0.15
        for: 30m
        labels: { severity: critical }
        annotations: { summary: "Menina data disk below 15% free" }
      - alert: MeninaDown
        expr: absent(menina_up) or menina_up == 0
        for: 5m
        labels: { severity: critical }
```

## Upgrades

`menina upgrade` verifies the artifact checksum, executes the staged binary
before replacing it (atomic, same-directory rename with a `.bak`), and reports
that services must be restarted and migrations run on next start. To install a
locally built binary (e.g. no published release), use
`MENINA_LOCAL_BINARY=/path/to/menina sh scripts/install.sh`.

## Docker (development)

The production model is native (binary + systemd). A container image and compose
stack are provided for development, demos and CI:

```bash
docker compose up --build
```

The `init` service runs `menina setup --non-interactive` once; `serve` exposes
the control socket and registry listener on `:9000`; `proxy` publishes
`:80`/`:443`; and a Zot container is included for local pushes. Build the image
directly with `make docker` (or `docker build -t menina:dev .`).

## Development

```bash
make build    # build ./bin/menina
make test     # go test ./...
make race     # go test -race ./...
make vet      # go vet ./...
```

The database uses text primary keys generated with
`lower(hex(randomblob(12)))` and `created_at`/`updated_at`/`deleted_at` columns
defaulting to `unixepoch()`, with supporting indexes on every lookup path.
