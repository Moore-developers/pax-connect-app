// swift-tools-version: 6.1
import PackageDescription

// Features：各页 ViewController 与页面专属 View。通过 Store 读状态、发 Intent，不直接调 PaxClient。
let package = Package(
  name: "Features",
  platforms: [.macOS("26.0")],
  products: [.library(name: "Features", targets: ["Features"])],
  dependencies: [
    .package(path: "../PaxCore"),
    .package(path: "../PaxUI"),
  ],
  targets: [
    .target(
      name: "Features", dependencies: ["PaxCore", "PaxUI"],
      resources: [.copy("Resources/ObjectIcons"), .copy("Resources/AIClients")])
  ]
)
