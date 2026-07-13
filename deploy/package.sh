#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${1:-}"
cd "$ROOT_DIR"

if [[ -z "$VERSION" ]]; then
  echo "usage: deploy/package.sh <version>" >&2
  exit 2
fi
if [[ ! "$VERSION" =~ ^[0-9A-Za-z._-]+$ ]]; then
  echo "version may only contain letters, digits, dots, underscores, and hyphens" >&2
  exit 2
fi

OUT_DIR="$ROOT_DIR/dist/release"
STAGE_DIR="$OUT_DIR/.stage"
rm -rf "$OUT_DIR"
mkdir -p "$STAGE_DIR"

cleanup() {
  rm -rf "$STAGE_DIR"
}
trap cleanup EXIT

build_agent() {
  local arch="$1"
  local package_name="control-tower-agent-${VERSION}-linux-${arch}"
  local package_dir="$STAGE_DIR/$package_name"

  mkdir -p "$package_dir"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build \
    -trimpath \
    -ldflags "-s -w -X main.agentVersion=$VERSION" \
    -o "$package_dir/control-tower-agent" \
    "$ROOT_DIR/agent/cmd/control-tower-agent"

  cp "$ROOT_DIR/deploy/install-agent.sh" "$package_dir/install-agent.sh"
  cp "$ROOT_DIR/deploy/control-tower-agent.service" "$package_dir/control-tower-agent.service"
  cp "$ROOT_DIR/deploy/agent.config.example" "$package_dir/agent.config.example"
  cp "$ROOT_DIR/deploy/agent.standalone.config.example" "$package_dir/agent.standalone.config.example"
  cp "$ROOT_DIR/agent/README.md" "$package_dir/README.md"
  sed -i 's/\r$//' "$package_dir/install-agent.sh"

  tar -C "$STAGE_DIR" -czf "$OUT_DIR/${package_name}.tar.gz" "$package_name"
}

build_agent amd64
build_agent arm64

(
  cd "$ROOT_DIR/webapp"
  corepack pnpm install --frozen-lockfile
  corepack pnpm build
)

SERVER_PACKAGE="control-tower-server-${VERSION}-linux-amd64"
SERVER_DIR="$STAGE_DIR/$SERVER_PACKAGE"
mkdir -p "$SERVER_DIR/server" "$SERVER_DIR/web/dist"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -trimpath \
  -ldflags "-s -w" \
  -o "$SERVER_DIR/control-tower-server" \
  "$ROOT_DIR/server/cmd/control-tower-server"
cp -R "$ROOT_DIR/server/migrations" "$SERVER_DIR/server/migrations"
cp -R "$ROOT_DIR/web/dist/desktop" "$SERVER_DIR/web/dist/desktop"
tar -C "$STAGE_DIR" -czf "$OUT_DIR/${SERVER_PACKAGE}.tar.gz" "$SERVER_PACKAGE"

(
  cd "$OUT_DIR"
  sha256sum ./*.tar.gz > SHA256SUMS
)

echo "release artifacts written to $OUT_DIR"
