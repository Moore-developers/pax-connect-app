#!/usr/bin/env bash
# Local ad-hoc Release candidate. Does not upload or change Gatekeeper settings.
# Usage: scripts/package-release.sh 0.1.0
set -euo pipefail
cd "$(dirname "$0")/.."
RELEASE_VERSION="${1:?usage: scripts/package-release.sh 0.1.0}"
[[ "$RELEASE_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
  echo 'error: version must be major.minor.patch' >&2; exit 1;
}
[ "$(uname -m)" = arm64 ] || { echo 'error: arm64 host required' >&2; exit 1; }
export PAX_BUILD_CONFIGURATION=Release PAX_RELEASE_VERSION="$RELEASE_VERSION"
# The candidate always uses the product identity and ad-hoc signing.
unset PAX_BUILD_BUNDLE_ID
VERSION="$RELEASE_VERSION" ./scripts/build-daemon.sh
./scripts/build-app.sh
APP="$PWD/build/DerivedData/Build/Products/Release/PaxConnect.app"
BUILD=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "$APP/Contents/Info.plist")
OUT="$PWD/build/releases/PaxConnect-${RELEASE_VERSION}-${BUILD}-arm64"
mkdir -p "$(dirname "$OUT")"
mkdir "$OUT"
STAGE=$(mktemp -d "$PWD/build/release-stage.XXXXXX")
trap 'rm -rf "$STAGE"' EXIT
# Copy first, so validation covers the exact bundle going into the image.
ditto "$APP" "$STAGE/PaxConnect.app"
ln -s /Applications "$STAGE/应用程序"
cp docs/release/INSTALL.zh-CN.html "$STAGE/安装与首次打开.html"
mkdir "$STAGE/说明与许可"
cp docs/release/RELEASE-NOTES.zh-CN.md "$STAGE/说明与许可/版本说明.md"
python3 scripts/release-materials.py "$STAGE/PaxConnect.app" "$OUT"
# Additional dependency notices are outside the signed bundle.
ditto "$OUT/licenses" "$STAGE/说明与许可/第三方许可"
cp "$OUT/manifest.json" "$STAGE/说明与许可/manifest.json"
# Pax's own split licenses (root overview, app, API, GPL text, trademarks).
PAXLIC="$STAGE/说明与许可/Pax 许可"
mkdir "$PAXLIC"
cp LICENSE "$PAXLIC/许可总览.txt"
cp macos/LICENSE "$PAXLIC/App-PolyForm-Noncommercial-1.0.0.md"
cp api/LICENSE "$PAXLIC/API-MIT.txt"
cp third_party/licenses/GPL-3.0.txt "$PAXLIC/GPL-3.0.txt"
cp TRADEMARKS.md "$PAXLIC/TRADEMARKS.md"
python3 scripts/create-release-dmg.py "$STAGE" "$OUT/PaxConnect-${RELEASE_VERSION}-arm64.dmg"
cp docs/release/INSTALL.zh-CN.html docs/release/RELEASE-NOTES.zh-CN.md "$OUT/"
(cd "$OUT" && shasum -a 256 *.dmg manifest.json *.html *.md > SHA256SUMS.txt)
python3 scripts/backup-release-data.py
echo "Release candidate: $OUT"
