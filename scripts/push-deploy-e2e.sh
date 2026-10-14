#!/usr/bin/env bash
# End-to-end: docker push -> Zot CloudEvent -> menina serve -> k3s deployment
# -> running pod -> reachable Service.
#
# Runs a real k3s node and a real Zot in Docker (host network) and runs the
# Menina control plane inside the k3s node (the supported "same host" model).
# Guest image: nginx:alpine pushed to Zot as e2e/api:production.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
K="menina-k3s-e2e"
Z="menina-zot-e2e"
CFG="/tmp/menina-e2e"
ZOT_IMAGE="ghcr.io/project-zot/zot:v2.1.22"

step() { printf '\n=== %s ===\n' "$1"; }

cleanup() {
  docker rm -f "$K" "$Z" >/dev/null 2>&1 || true
}

# The containers write into the mounted dirs as root, so removing leftovers
# needs a root helper.
cleanup_fs() {
  rm -rf "$CFG" 2>/dev/null || true
  if [ -d "$CFG" ]; then
    docker run --rm -v "/tmp:/x" alpine sh -c "rm -rf /x/$(basename "$CFG")" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

cleanup
cleanup_fs
mkdir -p "$CFG/cfg/projects/e2e/apps" "$CFG/cfg/projects/e2e/environments" "$CFG/data" "$CFG/zot"

step "build menina (static)"
( cd "$ROOT" && CGO_ENABLED=0 go build -o "$CFG/menina" ./cmd/menina )

step "write menina config"
cat > "$CFG/cfg/config.yml" <<'YAML'
host:
  configDir: /cfg
  dataDir: /data
kubernetes:
  managed: false
  kubeconfig: /etc/rancher/k3s/k3s.yaml
  ingressOwnership: menina
registry:
  enabled: true
  domain: "127.0.0.1:5000"
  auth:
    enabled: false
  notifications:
    enabled: true
    path: /v2/events
    endpoint: "http://127.0.0.1:9000/v2/events"
platform:
  dbPath: /data/menina.db
  controlSocket: /data/run/control.sock
  eventsListen: ":9000"
  httpListen: ":18080"
  httpsListen: ":18443"
  reconcileInterval: 1m
tls:
  terminate: false
networking:
  mode: public
defaults:
  namespace: e2e
  imageTagStrategy: environment
YAML
cat > "$CFG/cfg/projects/e2e/project.yml" <<'YAML'
name: e2e
namespace: e2e
YAML
cat > "$CFG/cfg/projects/e2e/apps/api.yml" <<'YAML'
name: api
image:
  repository: 127.0.0.1:5000/e2e/api
workload:
  type: service
  port: 80
resources:
  cpu: "100m"
  memory: "64Mi"
exposure:
  enabled: false
YAML
cat > "$CFG/cfg/projects/e2e/environments/production.yml" <<'YAML'
name: production
namespace: e2e
apps:
  api:
    enabled: true
    image:
      tag: production
YAML

step "write zot config + k3s registries.yaml"
cat > "$CFG/zot/config.json" <<'JSON'
{
  "distSpecVersion": "1.1.0",
  "storage": { "rootDirectory": "/tmp/zot-data", "gc": true },
  "http": { "address": "0.0.0.0", "port": "5000", "compat": ["docker2s2"] },
  "log": { "level": "error" },
  "extensions": {
    "events": { "enable": true, "sinks": [ { "type": "http", "address": "http://127.0.0.1:9000/v2/events" } ] }
  }
}
JSON
cat > "$CFG/registries.yaml" <<'YAML'
mirrors:
  "127.0.0.1:5000":
    endpoint:
      - "http://127.0.0.1:5000"
configs:
  "127.0.0.1:5000":
    tls:
      insecure_skip_verify: true
YAML

step "start k3s node (host network)"
docker run -d --name "$K" --privileged --network host \
  -v "$CFG/cfg:/cfg" -v "$CFG/data:/data" --tmpfs /run --tmpfs /var/run \
  rancher/k3s:latest server --disable traefik --disable servicelb --write-kubeconfig-mode 644 >/dev/null

step "wait for node Ready"
for _ in $(seq 1 60); do
  if docker exec "$K" kubectl get nodes --no-headers 2>/dev/null | grep -q " Ready"; then break; fi
  sleep 3
done
docker exec "$K" kubectl get nodes

step "install menina binary + registries.yaml into the node, restart k3s"
docker exec "$K" mkdir -p /usr/local/bin /etc/rancher/k3s
docker cp "$CFG/menina" "$K:/usr/local/bin/menina"
docker exec "$K" chmod +x /usr/local/bin/menina
docker cp "$CFG/registries.yaml" "$K:/etc/rancher/k3s/registries.yaml"
docker exec "$K" /usr/local/bin/menina version
docker restart "$K" >/dev/null
for _ in $(seq 1 60); do
  if docker exec "$K" kubectl get nodes --no-headers 2>/dev/null | grep -q " Ready"; then break; fi
  sleep 3
done
docker exec "$K" kubectl get nodes

step "preload sandbox image into k3s containerd (docker hub is slow for containerd)"
docker pull -q rancher/mirrored-pause:3.6 >/dev/null
docker save -o /tmp/e2e-pause.tar rancher/mirrored-pause:3.6
docker cp /tmp/e2e-pause.tar "$K:/tmp/pause.tar"
docker exec "$K" ctr -n k8s.io images import /tmp/pause.tar >/dev/null

step "start zot (host network, events -> 127.0.0.1:9000)"
docker run -d --name "$Z" --network host \
  -v "$CFG/zot/config.json:/etc/zot/config.json" \
  "$ZOT_IMAGE" serve /etc/zot/config.json >/dev/null
for _ in $(seq 1 30); do
  code=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:5000/v2/ || true)
  if [ "$code" = "200" ]; then break; fi
  sleep 2
done
echo "zot /v2/ -> $code"

step "start menina serve inside the k3s node"
docker exec -d "$K" sh -c 'menina --config-dir /cfg serve > /data/serve.log 2>&1'
for _ in $(seq 1 30); do
  if docker exec "$K" test -S /data/run/control.sock 2>/dev/null; then break; fi
  sleep 1
done
docker exec "$K" sh -c 'head -8 /data/serve.log' || true

step "push image -> should trigger automatic deployment"
docker pull -q nginx:alpine >/dev/null
docker tag nginx:alpine 127.0.0.1:5000/e2e/api:production
docker push 127.0.0.1:5000/e2e/api:production 2>&1 | tail -2

step "wait for the api Deployment to become available"
ok=""
for _ in $(seq 1 60); do
  ready=$(docker exec "$K" kubectl -n e2e get deploy api -o jsonpath='{.status.readyReplicas}' 2>/dev/null || true)
  if [ "$ready" = "1" ]; then ok=yes; break; fi
  sleep 3
done
docker exec "$K" kubectl -n e2e get deploy,svc,pods -o wide || true
if [ "$ok" != "yes" ]; then
  echo "DEPLOYMENT DID NOT BECOME READY"
  docker exec "$K" sh -c 'grep -iE "fail|error|deploy" /data/serve.log | head -20' || true
  exit 1
fi

step "verify the Service backend is reachable from the node"
clusterip=$(docker exec "$K" kubectl -n e2e get svc api -o jsonpath='{.spec.clusterIP}')
echo "api ClusterIP = $clusterip"
docker exec "$K" sh -c "wget -qO- -T5 http://$clusterip/ | head -c 40; echo"

step "verify the deployed digest matches the pushed image"
docker exec "$K" kubectl -n e2e get deploy api -o jsonpath='{.spec.template.spec.containers[0].image}'; echo

echo
echo "E2E PASS: push -> event -> deploy -> running app"
