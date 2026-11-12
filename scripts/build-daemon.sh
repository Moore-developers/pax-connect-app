#!/usr/bin/env bash
# 编译 pax-connectd 与 pax-core（arm64）到 build/。
#
#   ./scripts/build-daemon.sh
#   VERSION=1.2.3 ./scripts/build-daemon.sh        # 注入版本号（进启动自检日志）
#
# 共用协议传输标签；core 的 StartedService 必须有 with_clash_api。
# pax_tun_guard 与 scripts/go.sh 保留固定 sing-tun 路由保护。
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${VERSION:-0.1.0-dev}"
GO_TAGS="${GO_TAGS:-with_gvisor,with_quic,with_utls},with_clash_api,pax_tun_guard"
OUT="build/pax-connectd"
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"

# daemon 依赖生成的 gRPC 代码：缺失时先补齐，避免「改了 proto 忘了生成」的假失败。
if [ ! -d daemon/gen/proto/paxv1 ] || [ ! -d daemon/gen/proto/corev1 ]; then
  echo "--- 生成代码缺失，先跑 gen-proto.sh ---"
  ./scripts/gen-proto.sh
fi

mkdir -p build
echo "--- 编译 pax-connectd（${VERSION}，tags=${GO_TAGS}）---"
(cd daemon && GOOS=darwin GOARCH=arm64 ../scripts/go.sh build \
  -tags "${GO_TAGS}" \
  -trimpath \
  -ldflags "-s -w -X main.version=${VERSION}" \
  -o "../${OUT}" ./cmd/pax-connectd)

echo "  $(file "${OUT}" | sed 's/^[^:]*: //')"
echo "  $(du -h "${OUT}" | cut -f1)  ${OUT}"

echo "--- 编译 pax-core（${VERSION}，tags=${GO_TAGS}）---"
(cd daemon && GOOS=darwin GOARCH=arm64 ../scripts/go.sh build \
  -tags "${GO_TAGS}" -trimpath \
  -ldflags "-s -w -X main.version=${VERSION}" \
  -o ../build/pax-core ./cmd/pax-core)
