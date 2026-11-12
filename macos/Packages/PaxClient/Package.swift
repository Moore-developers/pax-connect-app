// swift-tools-version: 6.1
import PackageDescription

// PaxClient：gRPC 连接、重连、事件聚合，以及 proto → 领域模型的映射。
// 规则：不碰主线程；不把 proto 类型暴露给上层（code-standards.md §二）。
let package = Package(
  name: "PaxClient",
  platforms: [.macOS("26.0")],
  products: [.library(name: "PaxClient", targets: ["PaxClient"])],
  dependencies: [
    .package(path: "../PaxProto"),
    .package(url: "https://github.com/grpc/grpc-swift-2.git", from: "2.4.3"),
    .package(url: "https://github.com/grpc/grpc-swift-nio-transport.git", from: "2.10.0"),
  ],
  targets: [
    .target(
      name: "PaxClient",
      dependencies: [
        "PaxProto",
        .product(name: "GRPCCore", package: "grpc-swift-2"),
        .product(name: "GRPCNIOTransportHTTP2Posix", package: "grpc-swift-nio-transport"),
      ]
    ),
    .testTarget(name: "PaxClientTests", dependencies: ["PaxClient"]),
    // 开发用探针：不经 App 直接连 daemon，验证握手与事件流（本地服务先行的调试入口）。
    .executableTarget(name: "PaxProbe", dependencies: ["PaxClient"]),
  ]
)
