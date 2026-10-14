#!/usr/bin/env bash
# Reproducible end-to-end test of the host(Menina proxy) -> k8s ClusterIP path.
#
# It starts a real k3s server in Docker, builds the integration test binary,
# copies it INTO the k3s node container, and runs it there so the test shares
# the node's network namespace (the supported single-host deployment).
#
# Usage: scripts/k3s-net-e2e.sh
set -euo pipefail

CONTAINER="${CONTAINER:-menina-k3s-e2e}"
IMAGE="${IMAGE:-rancher/k3s:latest}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

cleanup() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  rm -f /tmp/menina-net.test
}
trap cleanup EXIT

echo "== starting k3s ($IMAGE) =="
docker run -d --name "$CONTAINER" --privileged --tmpfs /run --tmpfs /var/run \
  rancher/k3s:latest server --disable traefik --disable servicelb >/dev/null

echo "== waiting for the node to be Ready =="
for _ in $(seq 1 60); do
  if docker exec "$CONTAINER" kubectl get nodes --no-headers 2>/dev/null | grep -q " Ready"; then
    break
  fi
  sleep 3
done
docker exec "$CONTAINER" kubectl get nodes

echo "== building integration test binary =="
( cd "$ROOT" && CGO_ENABLED=0 go test -tags integration -c -o /tmp/menina-net.test ./tests/integration )

echo "== running the test inside the k3s node netns =="
docker cp /tmp/menina-net.test "$CONTAINER:/tmp/menina-net.test"
docker exec -e MENINA_K3S_E2E=1 "$CONTAINER" /tmp/menina-net.test -test.v -test.run TestHostToClusterIPEndToEnd

echo "== PASS =="
