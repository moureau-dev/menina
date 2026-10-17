#!/usr/bin/env sh
set -eu

# Menina client installer (Linux and macOS).
#
# Installs the cross-platform client binary that scaffolds/validates manifests,
# applies them over SSH, and builds/pushes images. It does not install the host
# control plane; use install.sh for that.

APP="menina-client"
REPO="${MENINA_REPO:-moureau-dev/menina}"
VERSION="${MENINA_CLIENT_VERSION:-latest}"
NO_MODIFY_PATH="${MENINA_CLIENT_NO_MODIFY_PATH:-0}"

info() { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"; }

detect_platform() {
  case "$(uname -s)" in
    Linux) OS="linux" ;;
    Darwin) OS="darwin" ;;
    *)
      die "unsupported OS '$(uname -s)'; on Windows use install-client.ps1"
      ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) die "unsupported architecture '$(uname -m)'; supported: amd64, arm64" ;;
  esac
}

choose_dir() {
  if [ -n "${MENINA_CLIENT_DIR:-}" ]; then
    BIN_DIR="$MENINA_CLIENT_DIR"
  elif [ "$(id -u)" -eq 0 ]; then
    BIN_DIR="/usr/local/bin"
  else
    BIN_DIR="${HOME}/.local/bin"
  fi
  if ! mkdir -p "$BIN_DIR" 2>/dev/null; then
    warn "cannot create $BIN_DIR; falling back to ${HOME}/.local/bin"
    BIN_DIR="${HOME}/.local/bin"
    mkdir -p "$BIN_DIR" || die "cannot create $BIN_DIR"
  fi
  [ -w "$BIN_DIR" ] || die "$BIN_DIR is not writable; set MENINA_CLIENT_DIR to a writable directory"
}

resolve_version() {
  if [ "$VERSION" != "latest" ]; then
    printf '%s' "$VERSION"
    return
  fi
  need sed
  curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1
}

hash_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    die "need sha256sum or shasum to verify the download"
  fi
}

download_and_install() {
  version="$1"
  asset="${APP}-${OS}-${ARCH}"
  base="https://github.com/${REPO}/releases/download/${version}"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT

  info "downloading ${asset} ${version}"
  curl -fsSL "${base}/${asset}" -o "${tmp}/${asset}" || die "download failed: ${base}/${asset}"
  curl -fsSL "${base}/checksums.txt" -o "${tmp}/checksums.txt" || die "could not download checksums.txt"

  expected="$(grep " ${asset}\$" "${tmp}/checksums.txt" | awk '{print $1}')"
  [ -n "$expected" ] || die "checksum for ${asset} not found in checksums.txt"
  actual="$(hash_file "${tmp}/${asset}")"
  [ "$expected" = "$actual" ] || die "checksum mismatch for ${asset}"

  info "checksum verified"
  cp "${tmp}/${asset}" "${BIN_DIR}/${APP}"
  chmod 0755 "${BIN_DIR}/${APP}"
}

install_local() {
  [ -x "$MENINA_CLIENT_LOCAL_BINARY" ] || die "MENINA_CLIENT_LOCAL_BINARY=$MENINA_CLIENT_LOCAL_BINARY is not executable"
  info "installing local binary $MENINA_CLIENT_LOCAL_BINARY (no download, no checksum)"
  cp "$MENINA_CLIENT_LOCAL_BINARY" "${BIN_DIR}/${APP}"
  chmod 0755 "${BIN_DIR}/${APP}"
}

modify_path() {
  case ":${PATH}:" in
    *":${BIN_DIR}:"*) return 0 ;;
  esac
  if [ "$NO_MODIFY_PATH" = "1" ]; then
    info "PATH not modified. Add this to your shell profile:"
    info "  export PATH=\"${BIN_DIR}:\$PATH\""
    return 0
  fi

  shell_name="$(basename "${SHELL:-sh}")"
  rc=""
  fish_syntax=0
  case "$shell_name" in
    zsh) rc="${HOME}/.zshrc" ;;
    bash)
      if [ "$OS" = "darwin" ]; then
        rc="${HOME}/.bash_profile"
      else
        rc="${HOME}/.bashrc"
      fi
      ;;
    fish)
      rc="${HOME}/.config/fish/config.fish"
      fish_syntax=1
      mkdir -p "$(dirname "$rc")"
      ;;
  esac

  if [ -z "$rc" ]; then
    info "Add this to your shell profile:"
    info "  export PATH=\"${BIN_DIR}:\$PATH\""
    return 0
  fi
  if grep -q "menina-client" "$rc" 2>/dev/null; then
    return 0
  fi

  if [ "$fish_syntax" = "1" ]; then
    printf '\n# >>> menina-client >>>\nfish_add_path "%s"\n# <<< menina-client <<<\n' "$BIN_DIR" >> "$rc"
  else
    printf '\n# >>> menina-client >>>\nexport PATH="%s:$PATH"\n# <<< menina-client <<<\n' "$BIN_DIR" >> "$rc"
  fi
  info "added ${BIN_DIR} to PATH in ${rc}; restart your shell"
}

main() {
  case "${1:-}" in
    -h|--help)
      info "usage: install-client.sh"
      info "  MENINA_CLIENT_VERSION   release tag (default: latest)"
      info "  MENINA_CLIENT_DIR       install directory"
      info "  MENINA_CLIENT_LOCAL_BINARY  install a local build"
      info "  MENINA_CLIENT_NO_MODIFY_PATH=1  do not edit shell profiles"
      info "  MENINA_REPO             GitHub repository"
      exit 0
      ;;
  esac
  need uname
  need curl
  need mktemp
  detect_platform
  choose_dir

  if [ -n "${MENINA_CLIENT_LOCAL_BINARY:-}" ]; then
    install_local
    version="$("${BIN_DIR}/${APP}" version 2>/dev/null || echo local)"
  else
    version="$(resolve_version)"
    [ -n "$version" ] || die "could not determine the latest release"
    download_and_install "$version"
  fi
  modify_path

  info ""
  info "Menina client ${version} installed to ${BIN_DIR}/${APP}"
  info "Next:"
  info "  menina-client init          # scaffold .menina.yml"
  info "  menina-client validate      # check it"
  info "  menina-client apply --host user@vps.example.com"
  info "  menina-client build --push  # build and push images"
}

main "$@"
