#!/usr/bin/env bash
# Verify the scoped Menina Kubernetes identity against a real k3s: the service
# account token is populated, the service identity can do its job, and it
# cannot perform cluster-admin actions.
set -euo pipefail

K="menina-rbac-e2e"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

cleanup() { docker rm -f "$K" >/dev/null 2>&1 || true; rm -f /tmp/menina-rbac.test; }
trap cleanup EXIT
cleanup

echo "== starting k3s =="
docker run -d --name "$K" --privileged --network host --tmpfs /run --tmpfs /var/run \
  rancher/k3s:latest server --disable traefik --disable servicelb >/dev/null
for _ in $(seq 1 60); do docker exec "$K" kubectl get nodes --no-headers 2>/dev/null | grep -q " Ready" && break; sleep 3; done
docker exec "$K" kubectl get nodes

echo "== build integration test =="
( cd "$ROOT" && CGO_ENABLED=0 go test -tags integration -c -o /tmp/menina-rbac.test ./tests/integration )

echo "== run RBAC verification inside the node =="
docker cp /tmp/menina-rbac.test "$K:/tmp/menina-rbac.test"
docker exec -e MENINA_K3S_E2E=1 -e KUBECONFIG=/etc/rancher/k3s/k3s.yaml \
  "$K" /tmp/menina-rbac.test -test.v -test.run TestProvisionedServiceIdentityIsScoped

echo "== RBAC E2E PASS =="
