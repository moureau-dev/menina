#!/usr/bin/env bash
# Verify private image pulls through the real k3s containerd runtime (not
# Docker), using a Menina-shaped registries.yaml:
#   - authenticated pull with no Docker login on the node
#   - credential rotation (new password + k3s reload)
#   - registry unavailable -> ImagePullBackOff -> recovery
#
# Usage: scripts/registry-pull-e2e.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
K="menina-pull-k3s"
Z="menina-pull-zot"
CFG="/tmp/menina-pull"
ZOT_IMAGE="ghcr.io/project-zot/zot:v2.1.22"
REG="127.0.0.1:5000"
USER="menina"
PASS1="pass-one-$$"
PASS2="pass-two-$$"

step() { printf '\n=== %s ===\n' "$1"; }

cleanup() { docker rm -f "$K" "$Z" >/dev/null 2>&1 || true; }
cleanup_fs() {
  rm -rf "$CFG" 2>/dev/null || true
  [ -d "$CFG" ] && docker run --rm -v "/tmp:/x" alpine sh -c "rm -rf /x/$(basename "$CFG")" >/dev/null 2>&1 || true
}
trap cleanup EXIT

cleanup; cleanup_fs
mkdir -p "$CFG/zot" "$CFG/htpasswd"

step "generate htpasswd (bcrypt) for zot"
docker run --rm -v "$CFG:/w" httpd:2-alpine htpasswd -nbB "$USER" "$PASS1" > "$CFG/htpasswd/htpasswd"

step "write zot config with auth (no events; we only test pulls)"
cat > "$CFG/zot/config.json" <<JSON
{
  "distSpecVersion": "1.1.0",
  "storage": { "rootDirectory": "/tmp/zot-data" },
  "http": { "address": "0.0.0.0", "port": "5000", "compat": ["docker2s2"],
            "auth": { "htpasswd": { "path": "/etc/zot/htpasswd" } } },
  "log": { "level": "error" }
}
JSON

step "start k3s (host network) + preload sandbox image"
docker run -d --name "$K" --privileged --network host --tmpfs /run --tmpfs /var/run \
  rancher/k3s:latest server --disable traefik --disable servicelb --write-kubeconfig-mode 644 >/dev/null
for _ in $(seq 1 60); do docker exec "$K" kubectl get nodes --no-headers 2>/dev/null | grep -q " Ready" && break; sleep 3; done
docker exec "$K" kubectl get nodes
docker pull -q rancher/mirrored-pause:3.6 >/dev/null
docker save -o "$CFG/pause.tar" rancher/mirrored-pause:3.6
docker cp "$CFG/pause.tar" "$K:/tmp/pause.tar"
docker exec "$K" ctr -n k8s.io images import /tmp/pause.tar >/dev/null

step "start zot with auth"
docker run -d --name "$Z" --network host \
  -v "$CFG/zot/config.json:/etc/zot/config.json" \
  -v "$CFG/htpasswd/htpasswd:/etc/zot/htpasswd" \
  "$ZOT_IMAGE" serve /etc/zot/config.json >/dev/null
for _ in $(seq 1 30); do [ "$(curl -s -o /dev/null -w '%{http_code}' "http://$REG/v2/" || true)" = "401" ] && break; sleep 2; done
echo "unauthenticated /v2/ -> $(curl -s -o /dev/null -w '%{http_code}' "http://$REG/v2/")"

step "write registries.yaml (Menina shape: mirror + auth + insecure) and reload k3s"
cat > "$CFG/registries.yaml" <<YAML
mirrors:
  "$REG":
    endpoint:
      - "http://$REG"
configs:
  "$REG":
    auth:
      username: "$USER"
      password: "$PASS1"
    tls:
      insecure_skip_verify: true
YAML
docker exec "$K" mkdir -p /etc/rancher/k3s
docker cp "$CFG/registries.yaml" "$K:/etc/rancher/k3s/registries.yaml"
docker exec "$K" chmod 0600 /etc/rancher/k3s/registries.yaml
echo "registries.yaml mode: $(docker exec "$K" stat -c '%a' /etc/rancher/k3s/registries.yaml)"
docker restart "$K" >/dev/null
for _ in $(seq 1 60); do docker exec "$K" kubectl get nodes --no-headers 2>/dev/null | grep -q " Ready" && break; sleep 3; done

step "push a private image (host docker login is only for pushing, not for k3s)"
docker pull -q nginx:alpine >/dev/null
echo "$PASS1" | docker login "$REG" -u "$USER" --password-stdin >/dev/null
docker tag nginx:alpine "$REG/e2e/api:production"
docker push "$REG/e2e/api:production" 2>&1 | tail -1

step "deploy on the node; containerd must pull using registries.yaml (no docker session)"
docker exec "$K" kubectl create deployment web --image="$REG/e2e/api:production" >/dev/null
docker exec "$K" kubectl expose deployment web --port=80 --target-port=80 >/dev/null
ok=""
for _ in $(seq 1 40); do
  r=$(docker exec "$K" kubectl get deploy web -o jsonpath='{.status.readyReplicas}' 2>/dev/null || true)
  [ "$r" = "1" ] && ok=yes && break
  sleep 3
done
docker exec "$K" kubectl get pods
[ "$ok" = "yes" ] || { echo "PRIVATE PULL FAILED"; docker exec "$K" kubectl describe pod -l app=web | grep -A4 Events; exit 1; }
echo "PRIVATE PULL OK (containerd pulled $REG/e2e/api:production with credentials)"

step "credential rotation: new password, reload k3s, pull a new tag"
docker run --rm -v "$CFG:/w" httpd:2-alpine htpasswd -nbB "$USER" "$PASS2" > "$CFG/htpasswd/htpasswd"
docker restart "$Z" >/dev/null
sleep 3
echo "$PASS2" | docker login "$REG" -u "$USER" --password-stdin >/dev/null
docker tag nginx:alpine "$REG/e2e/api:v2"
docker push "$REG/e2e/api:v2" 2>&1 | tail -1
sed -i "s/$PASS1/$PASS2/" "$CFG/registries.yaml"
docker cp "$CFG/registries.yaml" "$K:/etc/rancher/k3s/registries.yaml"
docker restart "$K" >/dev/null
for _ in $(seq 1 60); do docker exec "$K" kubectl get nodes --no-headers 2>/dev/null | grep -q " Ready" && break; sleep 3; done
docker exec "$K" kubectl set image deployment/web api="$REG/e2e/api:v2" >/dev/null
ok=""
for _ in $(seq 1 40); do
  img=$(docker exec "$K" kubectl get deploy web -o jsonpath='{.spec.template.spec.containers[0].image}')
  r=$(docker exec "$K" kubectl get deploy web -o jsonpath='{.status.readyReplicas}' 2>/dev/null || true)
  [ "$r" = "1" ] && [ "${img##*:}" = "v2" ] && ok=yes && break
  sleep 3
done
[ "$ok" = "yes" ] || { echo "ROTATED CREDENTIAL PULL FAILED"; docker exec "$K" kubectl describe pod -l app=web | grep -A4 Events; exit 1; }
echo "ROTATION OK (pulled after credential change + k3s reload)"

step "registry unavailable -> ImagePullBackOff, then recovery"
# Push a fresh tag so containerd has no cached copy and must contact the registry.
docker tag nginx:alpine "$REG/e2e/api:v3"
docker push "$REG/e2e/api:v3" 2>&1 | tail -1
docker stop "$Z" >/dev/null
docker exec "$K" kubectl create deployment down --image="$REG/e2e/api:v3" >/dev/null 2>&1 || true
for _ in $(seq 1 20); do
  st=$(docker exec "$K" kubectl get pods -l app=down -o jsonpath='{.items[0].status.containerStatuses[0].state.waiting.reason}' 2>/dev/null || true)
  [ "$st" = "ImagePullBackOff" ] || [ "$st" = "ErrImagePull" ] && break
  sleep 3
done
echo "registry down -> pod reason: $st"
docker start "$Z" >/dev/null
sleep 3
docker exec "$K" kubectl delete pod -l app=down >/dev/null 2>&1 || true
ok=""
for _ in $(seq 1 40); do
  r=$(docker exec "$K" kubectl get deploy down -o jsonpath='{.status.readyReplicas}' 2>/dev/null || true)
  [ "$r" = "1" ] && ok=yes && break
  sleep 3
done
[ "$ok" = "yes" ] || { echo "RECOVERY FAILED"; docker exec "$K" kubectl describe pod -l app=down | grep -A4 Events; exit 1; }
echo "RECOVERY OK (pull succeeded after the registry came back)"

echo
echo "REGISTRY PULL E2E PASS"
