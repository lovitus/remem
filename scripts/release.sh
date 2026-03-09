#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

VERSION="${1:-}"
if [[ -z "$VERSION" ]]; then
  echo "usage: $0 <version-tag-like-v0.3.2>"
  exit 1
fi

OUT_DIR="dist/release"
rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

build_one() {
  local goos="$1"
  local goarch="$2"
  local suffix="$3"
  local extra_ldflags="$4"
  local bin_name="remem${suffix}"
  local target="$OUT_DIR/${bin_name}"

  echo "building ${goos}/${goarch} -> ${target}"
  GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags "-s -w -X main.version=${VERSION} ${extra_ldflags}" -o "$target" ./cmd/remem
}

build_macos_dmg() {
  if [[ "$(uname -s)" != "Darwin" ]]; then
    echo "skip dmg build: host is not macOS"
    return
  fi
  if ! command -v hdiutil >/dev/null 2>&1; then
    echo "skip dmg build: hdiutil not found"
    return
  fi

  local mac_bin="$OUT_DIR/remem-darwin-arm64"
  if [[ ! -f "$mac_bin" ]]; then
    echo "skip dmg build: missing $mac_bin"
    return
  fi

  local stage_dir
  stage_dir="$(mktemp -d "${TMPDIR:-/tmp}/remem-dmg.XXXXXX")"
  local app_dir="$stage_dir/remem.app"
  local dmg_path="$OUT_DIR/remem-macos-arm64-${VERSION}.dmg"
  local short_version="${VERSION#v}"

  mkdir -p "$app_dir/Contents/MacOS" "$app_dir/Contents/Resources"
  cp "$mac_bin" "$app_dir/Contents/MacOS/remem"
  chmod +x "$app_dir/Contents/MacOS/remem"

  cat > "$app_dir/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key><string>en</string>
  <key>CFBundleDisplayName</key><string>remem</string>
  <key>CFBundleExecutable</key><string>remem</string>
  <key>CFBundleIdentifier</key><string>com.lovitus.remem</string>
  <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
  <key>CFBundleName</key><string>remem</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${short_version}</string>
  <key>CFBundleVersion</key><string>${VERSION}</string>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>LSUIElement</key><true/>
</dict>
</plist>
PLIST

  cp "$ROOT_DIR/docs/MACOS_DMG_README.txt" "$stage_dir/Read Me First.txt"
  ln -s /Applications "$stage_dir/Applications"

  rm -f "$dmg_path"
  echo "building dmg -> ${dmg_path}"
  env -i \
    PATH="$PATH" \
    HOME="$HOME" \
    TMPDIR="${TMPDIR:-/tmp}" \
    LANG=C \
    LC_ALL=C \
    LC_CTYPE=C \
    hdiutil create -volname "remem ${VERSION}" -srcfolder "$stage_dir" -ov -format UDZO "$dmg_path" >/dev/null

  rm -rf "$stage_dir"
}

build_one darwin arm64 "-darwin-arm64" ""
build_one windows amd64 "-windows-amd64.exe" "-H=windowsgui"
build_macos_dmg

(
  cd "$OUT_DIR"
  rm -f checksums.txt
  LC_ALL=C find . -maxdepth 1 -type f ! -name 'checksums.txt' -print | LC_ALL=C sort | while IFS= read -r f; do
    LC_ALL=C LANG=C shasum -a 256 "$f"
  done > checksums.txt
)

echo "release artifacts ready in $OUT_DIR"
ls -lh "$OUT_DIR"
