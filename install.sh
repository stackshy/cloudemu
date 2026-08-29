#!/bin/sh
# install.sh — one-line installer for the cloudemu CLI.
#
# Detects your OS/arch, downloads the matching release archive from GitHub,
# verifies its SHA-256 against the release's checksums.txt, and installs the
# `cloudemu` binary into a bin directory on your PATH.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/stackshy/cloudemu/HEAD/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/stackshy/cloudemu/HEAD/install.sh | sh -s -- v2.5.0
#   INSTALL_DIR="$HOME/bin" curl -fsSL https://raw.githubusercontent.com/stackshy/cloudemu/HEAD/install.sh | sh
#
# Env overrides:
#   INSTALL_DIR   target bin directory (default: /usr/local/bin, else $HOME/.local/bin)
#   VERSION       release tag to install (default: latest); a positional arg wins over this
set -eu

REPO="stackshy/cloudemu"
BINARY="cloudemu"

info() { printf '%s\n' "cloudemu: $*"; }
err() { printf '%s\n' "cloudemu: error: $*" >&2; exit 1; }

# --- pick an HTTP downloader ------------------------------------------------
if command -v curl >/dev/null 2>&1; then
  http_get() { curl -fsSL "$1"; }
  http_download() { curl -fsSL -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  http_get() { wget -qO- "$1"; }
  http_download() { wget -qO "$2" "$1"; }
else
  err "need curl or wget installed"
fi

# --- detect OS --------------------------------------------------------------
os="$(uname -s)"
case "$os" in
  Linux) os="linux" ;;
  Darwin) os="darwin" ;;
  *) err "unsupported OS: $os (only linux and darwin are supported)" ;;
esac

# --- detect arch (map uname -> goreleaser/goarch naming) --------------------
arch="$(uname -m)"
case "$arch" in
  x86_64 | amd64) arch="amd64" ;;
  aarch64 | arm64) arch="arm64" ;;
  *) err "unsupported architecture: $arch (only amd64 and arm64 are supported)" ;;
esac

# --- resolve version tag ----------------------------------------------------
tag="${1:-${VERSION:-}}"
if [ -z "$tag" ]; then
  info "resolving latest release..."
  tag="$(http_get "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' \
    | head -n1 \
    | sed 's/.*"tag_name"[^"]*"\([^"]*\)".*/\1/')"
  [ -n "$tag" ] || err "could not resolve the latest release tag from the GitHub API"
fi

# goreleaser archives use the version WITHOUT the leading "v"
version="${tag#v}"
archive="${BINARY}_${version}_${os}_${arch}.tar.gz"
base_url="https://github.com/${REPO}/releases/download/${tag}"

info "installing ${BINARY} ${tag} (${os}/${arch})"

# --- download into a temp dir -----------------------------------------------
tmp="$(mktemp -d 2>/dev/null || mktemp -d -t cloudemu)"
cleanup() { rm -rf "$tmp"; }
trap cleanup EXIT INT TERM

info "downloading ${archive}..."
http_download "${base_url}/${archive}" "${tmp}/${archive}" \
  || err "download failed: ${base_url}/${archive}"

info "downloading checksums.txt..."
http_download "${base_url}/checksums.txt" "${tmp}/checksums.txt" \
  || err "could not download checksums.txt from ${base_url}"

# --- verify sha256 ----------------------------------------------------------
expected="$(grep " ${archive}\$" "${tmp}/checksums.txt" | awk '{print $1}' | head -n1)"
[ -n "$expected" ] || err "no checksum entry for ${archive} in checksums.txt"

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "${tmp}/${archive}" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "${tmp}/${archive}" | awk '{print $1}')"
else
  err "need sha256sum or shasum to verify the download"
fi

if [ "$expected" != "$actual" ]; then
  err "checksum mismatch for ${archive} (expected ${expected}, got ${actual})"
fi
info "checksum verified"

# --- extract ----------------------------------------------------------------
tar -xzf "${tmp}/${archive}" -C "${tmp}" "${BINARY}" \
  || err "failed to extract ${BINARY} from ${archive}"
[ -f "${tmp}/${BINARY}" ] || err "${BINARY} not found in ${archive}"
chmod +x "${tmp}/${BINARY}"

# --- choose install dir + install -------------------------------------------
install_to() {
  dir="$1"
  if [ -d "$dir" ] && [ -w "$dir" ]; then
    mv "${tmp}/${BINARY}" "${dir}/${BINARY}"
    return 0
  fi
  return 1
}

path_note=""
if [ -n "${INSTALL_DIR:-}" ]; then
  mkdir -p "$INSTALL_DIR" || err "cannot create INSTALL_DIR: $INSTALL_DIR"
  install_to "$INSTALL_DIR" || err "cannot write to INSTALL_DIR: $INSTALL_DIR"
  dest="$INSTALL_DIR"
elif install_to "/usr/local/bin"; then
  dest="/usr/local/bin"
elif command -v sudo >/dev/null 2>&1 && [ -d "/usr/local/bin" ] \
    && sudo mv "${tmp}/${BINARY}" "/usr/local/bin/${BINARY}"; then
  dest="/usr/local/bin"
else
  dest="${HOME}/.local/bin"
  mkdir -p "$dest" || err "cannot create ${dest}"
  install_to "$dest" || err "cannot write to ${dest}"
  case ":${PATH}:" in
    *":${dest}:"*) ;;
    *) path_note="  Add it to your PATH:  export PATH=\"${dest}:\$PATH\"" ;;
  esac
fi

info "installed ${BINARY} to ${dest}/${BINARY}"
[ -z "$path_note" ] || printf '%s\n' "$path_note"
info "run '${BINARY} version' to verify"
