#!/usr/bin/env bash
# make relaunch：用最新代码重建并重启 Debug App（原 AGENTS.md「默认交付」）。
#   1. 核验旧 Debug bundle 的 CFBundleIdentifier 是本项目的（dev.moore.PaxConnect，
#      或 PAX_BUILD_BUNDLE_ID 覆盖值）；不是则中止，不删除任何文件
#   2. 正常退出运行中的实例（scripts/quiesce-build-app.py：NSRunningApplication.terminate，
#      走 AppDelegate 的收尾，并等待其内嵌进程退出；超时则拒绝覆盖）
#   3. 只删除该已核验的 Debug bundle；不碰其它应用
#   4. make app（build-app.sh 负责递增构建号，状态在 build/app-build-number，本脚本不重置）
#   5. 启动新构建，并打印旧 -> 新构建号
# 保留：用户配置、线路凭据、观测历史（均在 bundle 之外）和构建号状态。
set -euo pipefail
cd "$(dirname "$0")/.."

BUNDLE="$PWD/build/DerivedData/Build/Products/Debug/PaxConnect.app"
EXPECTED_ID="${PAX_BUILD_BUNDLE_ID:-dev.moore.PaxConnect}"

plist_value() { # <key>；bundle 不存在或缺键时输出空
  python3 -I - "$BUNDLE/Contents/Info.plist" "$1" <<'PY'
import plistlib, sys
try:
    with open(sys.argv[1], 'rb') as f:
        print(plistlib.load(f).get(sys.argv[2], ''))
except FileNotFoundError:
    pass
PY
}

old_build=none
if [ -d "$BUNDLE" ]; then
  found_id=$(plist_value CFBundleIdentifier)
  if [ "$found_id" != "$EXPECTED_ID" ]; then
    echo "error: $BUNDLE 的 CFBundleIdentifier 是 '${found_id}'，不是 '${EXPECTED_ID}'；拒绝删除" >&2
    exit 1
  fi
  old_build=$(plist_value CFBundleVersion)
  python3 scripts/quiesce-build-app.py "$BUNDLE"
  rm -rf "$BUNDLE"
fi

make app

new_build=$(plist_value CFBundleVersion)
[ -n "$new_build" ] || { echo "error: 构建后缺少 $BUNDLE" >&2; exit 1; }
open "$BUNDLE"
echo "relaunch: 构建号 ${old_build} -> ${new_build}"
