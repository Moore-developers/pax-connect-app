// swift-tools-version: 6.1
import PackageDescription

// PaxUI：设计 token、基础控件、PaxCharts。不认识业务 Store，只接收值（code-standards.md §二）。
let package = Package(
  name: "PaxUI",
  platforms: [.macOS("26.0")],
  products: [.library(name: "PaxUI", targets: ["PaxUI"])],
  targets: [
    .target(name: "PaxUI", resources: [.copy("Resources/RouteIcons")])
  ]
)
