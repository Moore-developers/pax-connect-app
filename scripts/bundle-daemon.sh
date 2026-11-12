#!/usr/bin/env bash
# 把已编译的 pax-connectd 放进 App bundle 的 Contents/MacOS/ 并重新签名。
#
# 由 Xcode 的 Run Script 阶段调用（架构 §17：由内向外签名 daemon → App）：
#
#   "${SRCROOT}/../scripts/bundle-daemon.sh"
#
# Xcode 提供 BUILT_PRODUCTS_DIR / CONTENTS_FOLDER_PATH / EXPANDED_CODE_SIGN_IDENTITY；
# 在终端里手动跑需要自己带上这些环境变量。daemon 二进制由 `make daemon` 或
# `./scripts/build-daemon.sh` 先构建好——本脚本刻意不在 Xcode 里触发 Go 构建，
# 因为 Xcode 的构建沙箱不适合下载 Go module。
set -euo pipefail
cd "$(dirname "$0")/.."

SOURCE="${SOURCE:-build/pax-connectd}"
: "${BUILT_PRODUCTS_DIR:?需要在 Xcode 构建环境里运行（或手动设置 BUILT_PRODUCTS_DIR）}"
: "${CONTENTS_FOLDER_PATH:?需要在 Xcode 构建环境里运行（或手动设置 CONTENTS_FOLDER_PATH）}"

DESTINATION="${BUILT_PRODUCTS_DIR}/${CONTENTS_FOLDER_PATH}/MacOS"
if [ ! -x "${SOURCE}" ]; then
  echo "error: 找不到 ${SOURCE}；先运行 make daemon" >&2
  exit 1
fi

mkdir -p "${DESTINATION}"
cp "${SOURCE}" "${DESTINATION}/pax-connectd"

# daemon 与 App 用同一签名身份（由内向外），身份为空时退回 ad-hoc（S0-② 实测可用）。
IDENTITY="${EXPANDED_CODE_SIGN_IDENTITY:--}"
[ -n "${IDENTITY}" ] || IDENTITY="-"
echo "codesign pax-connectd with identity=${IDENTITY}"
codesign --force --options runtime --timestamp=none --sign "${IDENTITY}" "${DESTINATION}/pax-connectd"

test -x build/pax-core || { echo "error: missing pax-core; run make daemon" >&2; exit 1; }
cp build/pax-core "${DESTINATION}/pax-core"
codesign --force --options runtime --timestamp=none --sign "${IDENTITY}" "${DESTINATION}/pax-core"

# Hash the signed executable and the exact plist copied by Xcode, before outer App signing.
RESOURCES="${BUILT_PRODUCTS_DIR}/${CONTENTS_FOLDER_PATH}/Resources"
mkdir -p "${RESOURCES}"
shasum -a 256 "${DESTINATION}/pax-core" | awk '{print $1}' > "${RESOURCES}/core-fingerprint.txt"
cat "${DESTINATION}/pax-connectd" macos/PaxConnect/Resources/LaunchDaemons/dev.moore.pax-connect.daemon.plist | shasum -a 256 | awk '{print $1}' > "${RESOURCES}/daemon-fingerprint.txt"
TASK_DAEMON_VERSION=$("${DESTINATION}/pax-connectd" --version)
[ -n "$TASK_DAEMON_VERSION" ] || { echo "error: embedded daemon version is empty" >&2; exit 1; }
printf '%s\n' "$TASK_DAEMON_VERSION" > "${RESOURCES}/daemon-version.txt"
# Both embedded instances must be built from the same pinned release.
TASK_KERNEL_VERSION=$("${DESTINATION}/pax-connectd" --kernel-version)
test "$TASK_KERNEL_VERSION" = "$("${DESTINATION}/pax-core" --kernel-version)" || {
  echo "error: embedded kernel versions differ" >&2
  exit 1
}
printf '%s\n' "$TASK_KERNEL_VERSION" > "${RESOURCES}/kernel-version.txt"
# Static pinned routing assets and attribution, no credential file is bundled.
mkdir -p "${RESOURCES}/rulesets" "${RESOURCES}/licenses"
cp rulesets/*.srs rulesets/manifest.json "${RESOURCES}/rulesets/"
cp third_party/licenses/*.txt "${RESOURCES}/licenses/"
