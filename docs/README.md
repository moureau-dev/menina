# Menina documentation

- [Client CLI](CLIENT-CLI.md) — cross-platform client, `.menina.yml` manifests,
  apply over SSH, build/push, CI patterns, and limitations.
- [CLI reference](CLI.md) — commands, interactive flows, and the
  environment/scaling model.
- [Configuration & notifications](CONFIGURATION.md) — file layout, merge rules,
  scaling, deploy webhooks.
- [Proxy](PROXY.md) — how routing works, TLS, networking modes, caching,
  security, and supported protocols.
- [Kubernetes (k3s)](KUBERNETES.md) — how Menina provisions and uses k3s:
  namespaces, RBAC, workloads, the registry, and reconciliation.
- [Registry](REGISTRY.md) — the bundled Zot registry: auth, push, digest
  pinning, retention, and push-triggered deploys.
- [Operations](OPERATIONS.md) — privileges, security, backups, upgrades, Docker,
  and development.
- [Repository structure](STRUCTURE.md) — package layout and conventions for
  contributors and agents.

More docs will be added here over time. The project overview and quick start
live in the top-level [README](../README.md).
