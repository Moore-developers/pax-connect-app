#!/usr/bin/env bash
# 生成 App协议的Go/Swift，以及仅paxd/core内部协议的Go代码。
# 工具版本锁定在下面，保证两端生成物可复现。
#
#   ./scripts/gen-proto.sh
#
# 依赖：
#   protoc              —— brew install protobuf（本机 /opt/homebrew/include 提供 well-known types）
#   protoc-gen-go       —— go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.6
#   protoc-gen-go-grpc  —— go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
#   protoc-gen-swift    —— 见下方 SWIFT_PROTOBUF_VERSION
#   protoc-gen-grpc-swift-2 —— 见下方 GRPC_SWIFT_PROTOBUF_VERSION
# 后两个 Swift 插件由本仓库的 .tools/bin 提供（从锁定版本的源码构建，不进版本库）：
#   swift build --product protoc-gen-swift          # 在 swift-protobuf 检出目录
#   swift build --product protoc-gen-grpc-swift-2   # 在 grpc-swift-protobuf 检出目录
set -euo pipefail
cd "$(dirname "$0")/.."

PROTOC_GEN_GO_VERSION=v1.36.6
PROTOC_GEN_GO_GRPC_VERSION=v1.5.1
SWIFT_PROTOBUF_VERSION=1.38.1
GRPC_SWIFT_PROTOBUF_VERSION=2.4.1

GO_MODULE=pax-connect/daemon
PROTO_DIR=api
PROTO="${PROTO_DIR}/pax.proto"
GO_OUT=daemon
SWIFT_OUT=macos/Packages/PaxProto/Sources/PaxProto/Generated
# protoc 自带的 well-known types（timestamp.proto 等）
WELL_KNOWN_INCLUDE="$(dirname "$(dirname "$(command -v protoc)")")/include"
[ -d "${WELL_KNOWN_INCLUDE}/google/protobuf" ] || WELL_KNOWN_INCLUDE=/opt/homebrew/include

PATH="$(pwd)/.tools/bin:$(go env GOPATH)/bin:${PATH}"

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "缺少 $1；安装方式见本脚本头部注释" >&2
    exit 1
  }
}
require protoc
require protoc-gen-go
require protoc-gen-go-grpc
require protoc-gen-swift
require protoc-gen-grpc-swift-2

echo "--- Go（daemon）---"
mkdir -p "${GO_OUT}"
protoc -I "${PROTO_DIR}" -I "${WELL_KNOWN_INCLUDE}" \
  --go_out="${GO_OUT}" --go_opt=module="${GO_MODULE}" \
  --go-grpc_out="${GO_OUT}" --go-grpc_opt=module="${GO_MODULE}" \
  "${PROTO}" "${PROTO_DIR}/operation.proto" "${PROTO_DIR}/traffic.proto" "${PROTO_DIR}/privacy.proto" "${PROTO_DIR}/route_exit.proto" "${PROTO_DIR}/route_usage.proto" "${PROTO_DIR}/ai_profiles.proto" "${PROTO_DIR}/ai_usage.proto" "${PROTO_DIR}/network_diagnostics.proto"
find "${GO_OUT}" -name '*.go' | sort | sed 's/^/  /'

echo "--- Go（internal paxd/core only）---"
protoc -I "${PROTO_DIR}" -I "${WELL_KNOWN_INCLUDE}" \
  --go_out="${GO_OUT}" --go_opt=module="${GO_MODULE}" \
  --go-grpc_out="${GO_OUT}" --go-grpc_opt=module="${GO_MODULE}" \
  "${PROTO_DIR}/pax_core.proto"

echo "--- Swift（PaxProto）---"
mkdir -p "${SWIFT_OUT}"
protoc -I "${PROTO_DIR}" -I "${WELL_KNOWN_INCLUDE}" \
  --swift_out="${SWIFT_OUT}" --swift_opt=Visibility=Public \
  --grpc-swift-2_out="${SWIFT_OUT}" \
  --grpc-swift-2_opt=Visibility=Public \
  --grpc-swift-2_opt=Client=true \
  --grpc-swift-2_opt=Server=false \
  "${PROTO}" "${PROTO_DIR}/operation.proto" "${PROTO_DIR}/traffic.proto" "${PROTO_DIR}/privacy.proto" "${PROTO_DIR}/route_exit.proto" "${PROTO_DIR}/route_usage.proto" "${PROTO_DIR}/ai_profiles.proto" "${PROTO_DIR}/ai_usage.proto" "${PROTO_DIR}/network_diagnostics.proto"
find "${SWIFT_OUT}" -name '*.swift' | sort | sed 's/^/  /'

echo "--- 版本 ---"
echo "  protoc: $(protoc --version)"
echo "  protoc-gen-go: ${PROTOC_GEN_GO_VERSION}"
echo "  protoc-gen-go-grpc: ${PROTOC_GEN_GO_GRPC_VERSION}"
echo "  protoc-gen-swift: swift-protobuf ${SWIFT_PROTOBUF_VERSION}"
echo "  protoc-gen-grpc-swift-2: grpc-swift-protobuf ${GRPC_SWIFT_PROTOBUF_VERSION}"
