#!/usr/bin/env bash
# Build tsocial for the VPS architecture, copy it across, swap the binary,
# and restart the systemd unit. The SQLite migrations are idempotent, so a
# restart-in-place is safe.
#
# Usage:
#   VPS=deploy@your-vps ./deploy.sh           # default: deploy tsocial
#   VPS=deploy@your-vps ./deploy.sh admin     # also deploy tsocial-admin
#   ARCH=arm64 VPS=deploy@your-vps ./deploy.sh
#
# Required env:
#   VPS    — SSH target (user@host or ~/.ssh/config alias)
#
# Optional env:
#   ARCH   — GOARCH for the VPS (default: amd64; use arm64 for ARM VPSes)
#   GOOS   — GOOS for the VPS    (default: linux)

set -euo pipefail

: "${VPS:?set VPS=user@host (or ~/.ssh/config alias)}"
ARCH="${ARCH:-amd64}"
GOOS="${GOOS:-linux}"

DIST_DIR="dist"
mkdir -p "$DIST_DIR"

build() {
    local cmd="$1"
    echo "→ build $cmd ($GOOS/$ARCH)"
    GOOS="$GOOS" GOARCH="$ARCH" CGO_ENABLED=0 \
        go build -trimpath -ldflags="-s -w" -o "$DIST_DIR/$cmd" "./cmd/$cmd"
}

deploy_binary() {
    local cmd="$1"
    echo "→ ship $cmd to $VPS"
    scp -q "$DIST_DIR/$cmd" "$VPS:/tmp/$cmd.new"
    ssh "$VPS" "sudo install -m 0755 /tmp/$cmd.new /usr/local/bin/$cmd && rm -f /tmp/$cmd.new"
}

# Always build + ship the server binary.
build tsocial
deploy_binary tsocial

# Optionally also refresh the admin CLI.
if [[ "${1:-}" == "admin" ]]; then
    build tsocial-admin
    deploy_binary tsocial-admin
fi

echo "→ restart tsocial"
ssh "$VPS" '
    sudo systemctl restart tsocial &&
    sleep 1 &&
    systemctl is-active --quiet tsocial && echo "✓ active" || {
        echo "✗ service failed to start; recent logs:"
        sudo journalctl -u tsocial -n 30 --no-pager
        exit 1
    }
'

echo "deploy complete: $VPS"
