#!/usr/bin/env bash
# A new local package must have a new CFBundleVersion even when marketing version is unchanged.
set -euo pipefail
cd "$(dirname "$0")/.."
CONFIGURATION="${PAX_BUILD_CONFIGURATION:-Debug}"
case "$CONFIGURATION" in Debug|Release) ;; *) echo "error: invalid build configuration" >&2; exit 1 ;; esac
python3 scripts/quiesce-build-app.py "$PWD/build/DerivedData/Build/Products/$CONFIGURATION/PaxConnect.app"
mkdir -p build
BUILD_NUMBER=$(python3 - <<'PY'
import fcntl
import plistlib
from pathlib import Path

state = Path('build/app-build-number')
with state.open('a+') as file:
    fcntl.flock(file, fcntl.LOCK_EX)
    file.seek(0)
    previous = file.read().strip()
    installed = Path('/Applications/PaxConnect.app/Contents/Info.plist')
    number = max(2, int(previous) if previous else 0)
    if installed.exists():
        info = plistlib.loads(installed.read_bytes())
        if info.get('CFBundleIdentifier') == 'dev.moore.PaxConnect':
            value = info.get('CFBundleVersion', '')
            if str(value).isdigit():
                number = max(number, int(value))
    number += 1
    if number > 9999:
        raise SystemExit('Local build sequence exhausted; advance versioning policy before packaging.')
    file.seek(0)
    file.truncate()
    file.write(str(number) + '\n')
    file.flush()
    print(number)
PY
)
set --
if [ -n "${PAX_BUILD_BUNDLE_ID:-}" ]; then
  set -- "$@" "PRODUCT_BUNDLE_IDENTIFIER=${PAX_BUILD_BUNDLE_ID}"
fi
if [ -n "${PAX_RELEASE_VERSION:-}" ]; then
  set -- "$@" "MARKETING_VERSION=${PAX_RELEASE_VERSION}"
fi
xcodebuild -project macos/PaxConnect.xcodeproj -scheme PaxConnect \
  -configuration "$CONFIGURATION" -derivedDataPath build/DerivedData \
  CURRENT_PROJECT_VERSION="${BUILD_NUMBER}" "$@" build
