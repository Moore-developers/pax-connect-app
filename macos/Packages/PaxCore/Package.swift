// swift-tools-version: 6.1
import PackageDescription

// PaxCore：领域模型的对外出口（经 PaxClient 定义，见 Reexports.swift 的说明）、各领域 Store 与 Intent。
// 规则：不 import AppKit，可在纯单元测试里跑（code-standards.md §二）。
let package = Package(
  name: "PaxCore",
  platforms: [.macOS("26.0")],
  products: [.library(name: "PaxCore", targets: ["PaxCore"])],
  dependencies: [
    .package(path: "../PaxClient")
  ],
  targets: [
    .target(name: "PaxCore", dependencies: ["PaxClient"]),
    .testTarget(name: "PaxCoreTests", dependencies: ["PaxCore"]),
  ]
)
