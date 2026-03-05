#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

VERSION="${1:-}"
if [[ -z "$VERSION" ]]; then
  echo "usage: $0 <version-tag-like-v0.1.0>"
  exit 1
fi

OUT_DIR="dist/release"
rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

build_one() {
  local goos="$1"
  local goarch="$2"
  local suffix="$3"
  local bin_name="remem-guard${suffix}"
  local target="$OUT_DIR/${bin_name}"

  echo "building ${goos}/${goarch} -> ${target}"
  GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o "$target" ./cmd/remem-guard
}

build_one darwin arm64 "-darwin-arm64"
build_one windows amd64 "-windows-amd64.exe"

( cd "$OUT_DIR" && LC_ALL=C shasum -a 256 * > checksums.txt )

echo "release artifacts ready in $OUT_DIR"
ls -lh "$OUT_DIR"
