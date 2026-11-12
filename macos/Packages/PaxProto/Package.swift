// swift-tools-version: 6.1
import PackageDescription

// PaxProto：api/pax.proto 的 Swift 生成物（scripts/gen-proto.sh 产出）。
// 规则：只有 PaxClient 可以 import 本模块（code-standards.md §二）。
let package = Package(
  name: "PaxProto",
  platforms: [.macOS("26.0")],
  products: [.library(name: "PaxProto", targets: ["PaxProto"])],
  dependencies: [
    .package(url: "https://github.com/grpc/grpc-swift-2.git", from: "2.4.3"),
    .package(url: "https://github.com/grpc/grpc-swift-protobuf.git", from: "2.4.1"),
    .package(url: "https://github.com/apple/swift-protobuf.git", from: "1.38.0"),
  ],
  targets: [
    .target(
      name: "PaxProto",
      dependencies: [
        .product(name: "GRPCCore", package: "grpc-swift-2"),
        .product(name: "GRPCProtobuf", package: "grpc-swift-protobuf"),
        .product(name: "SwiftProtobuf", package: "swift-protobuf"),
      ]
    )
  ]
)
