#!/usr/bin/env sh
set -eu

# Menina installer.
#
# The installer INSTALLS; it does NOT provision.
# It downloads a verified native binary, installs shell completion and the
# systemd unit templates, then stops. Run `menina setup` and `menina deploy`
# explicitly afterwards.

APP="menina"
REPO="${MENINA_REPO:-moureau-dev/menina}"
VERSION="${MENINA_VERSION:-latest}"
PREFIX="${MENINA_PREFIX:-/usr/local}"
BIN_DIR="${PREFIX}/bin"
UNIT_DIR="${MENINA_UNIT_DIR:-/etc/systemd/system}"
SHARE_DIR="/usr/local/share/menina"

info() { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"; }

detect_platform() {
  os="$(uname -s)"
  [ "$os" = "Linux" ] || die "unsupported OS '$os'; Menina currently supports Ubuntu Server on amd64/arm64"
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) die "unsupported architecture '$arch'; supported: amd64, arm64" ;;
  esac
}

check_prereqs() {
  need uname
  need curl
  need sha256sum
  if command -v systemctl >/dev/null 2>&1; then
    HAS_SYSTEMD=1
  else
    HAS_SYSTEMD=0
  fi
  if command -v k3s >/dev/null 2>&1; then
    info "detected an existing k3s installation; it will be left untouched"
  fi
  avail_kb="$(df -Pk / | awk 'NR==2 {print $4}')"
  if [ "${avail_kb:-0}" -lt 1048576 ]; then
    warn "less than 1GiB free disk space on /"
  fi
}

resolve_version() {
  if [ "$VERSION" != "latest" ]; then
    echo "$VERSION"
    return
  fi
  need sed
  curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1
}

download_and_verify() {
  version="$1"
  asset="${APP}-linux-${ARCH}"
  base="https://github.com/${REPO}/releases/download/${version}"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT

  info "downloading ${asset} ${version}"
  curl -fsSL "${base}/${asset}" -o "${tmp}/${asset}"
  curl -fsSL "${base}/checksums.txt" -o "${tmp}/checksums.txt"

  expected="$(grep " ${asset}\$" "${tmp}/checksums.txt" | awk '{print $1}')"
  [ -n "$expected" ] || die "checksum for ${asset} not found in checksums.txt"
  actual="$(sha256sum "${tmp}/${asset}" | awk '{print $1}')"
  [ "$expected" = "$actual" ] || die "checksum mismatch for ${asset}"

  info "checksum verified"
  install -m 0755 "${tmp}/${asset}" "${BIN_DIR}/${APP}"
}

install_completion() {
  if "${BIN_DIR}/${APP}" install --system >/dev/null 2>&1; then
    info "installed shell completion (bash, zsh, fish)"
  else
    warn "could not install system shell completion; run '$APP install' as your user"
  fi
}

create_service_user() {
  if id "$APP" >/dev/null 2>&1; then
    return 0
  fi
  if command -v useradd >/dev/null 2>&1; then
    useradd --system --no-create-home --shell /usr/sbin/nologin --user-group "$APP" \
      || warn "could not create system user '$APP' with useradd"
  elif command -v adduser >/dev/null 2>&1; then
    adduser --system --no-create-home --group "$APP" \
      || warn "could not create system user '$APP' with adduser"
  else
    warn "no useradd/adduser found; create the '$APP' system user before running '$APP deploy'"
  fi
  info "ensured system user '$APP'"
  if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != "root" ]; then
    if usermod -aG "$APP" "$SUDO_USER" >/dev/null 2>&1; then
      info "added '$SUDO_USER' to group '$APP' (log out and back in to use the CLI without sudo)"
    else
      warn "could not add '$SUDO_USER' to group '$APP'; do it manually: sudo usermod -aG $APP $SUDO_USER"
    fi
  fi
}

install_unit_templates() {
  mkdir -p "$SHARE_DIR/systemd"
  cat > "${SHARE_DIR}/systemd/menina-serve.service" <<'EOF'
[Unit]
Description=Menina control plane
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=@USER@
Group=@GROUP@
ExecStart=@BINARY@ --config-dir @CONFIG_DIR@ serve
Restart=on-failure
RestartSec=3
RuntimeDirectory=menina
StateDirectory=menina
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF
  cat > "${SHARE_DIR}/systemd/menina-proxy.service" <<'EOF'
[Unit]
Description=Menina reverse proxy
After=network-online.target menina-serve.service
Wants=network-online.target
PartOf=menina-serve.service

[Service]
Type=simple
User=@USER@
Group=@GROUP@
ExecStart=@BINARY@ --config-dir @CONFIG_DIR@ proxy
Restart=on-failure
RestartSec=3
RuntimeDirectory=menina
StateDirectory=menina
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF
  info "installed systemd unit templates to ${SHARE_DIR}/systemd"
}

main() {
  [ "$(id -u)" -eq 0 ] || die "installing to ${BIN_DIR} requires root; re-run with sudo"
  detect_platform
  check_prereqs
  mkdir -p "$BIN_DIR"
  version="$(resolve_version)"
  [ -n "$version" ] || die "could not determine the latest release version"
  download_and_verify "$version"
  create_service_user
  install_completion
  install_unit_templates

  info ""
  info "Menina ${version} installed."
  info "Next steps:"
  info "  1. menina setup    # configure the host and platform"
  info "  2. menina deploy   # reconcile infrastructure and services"
}

main "$@"
