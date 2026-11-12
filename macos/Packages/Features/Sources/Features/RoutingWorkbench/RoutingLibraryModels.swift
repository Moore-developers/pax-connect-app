import Foundation

extension RoutingWorkbenchStore {
  enum PackageKind: String, CaseIterable {
    case priority = "优先规则"
    case local = "自己维护"
    case subscription = "从链接订阅"
    case geo = "内置"
    case adblock = "内置广告拦截"
  }
  enum Method: String, CaseIterable {
    case application = "应用"
    case domain = "精确域名"
    case domainSuffix = "域名及子域"
    case ip = "单 IP"
    case cidr = "IP 网段"
    case builtin = "内置分类"
    case source = "订阅条件"
  }
  enum Destination: Equatable {
    case followDefault, direct, global
    case route(String)
    case block
  }
  struct Rule: Equatable, Identifiable {
    var id: String
    var method: Method
    var target: String
    var destination: Destination
    var enabled = true
    var signature: String { method.rawValue + "|" + target }
  }
  struct RulePackage: Equatable, Identifiable {
    let id: String
    var name: String
    let kind: PackageKind
    var destination: Destination
    var enabled = true
    var rules: [Rule]
    var source = ""
    var version = "2026.10.07"
    var previousVersion: String?
    var previousRules: [Rule]?
    var automatic = true
    var updating = false
    var updateMessage = ""
    var updatedAt: Date? = nil
  }
  struct PackageDraft {
    var id: String?
    var name = ""
    var kind: PackageKind = .local
    var destination: Destination = .direct
    var source = ""
  }
  struct RuleDraft {
    var id: String?
    var packageID: String
    var method: Method = .domain
    var target = ""
    var destination: Destination = .direct
    var selectedDomains: [String] = []
    var importDomains = false
    var domainMethod: Method = .domain
  }
  struct Preview {
    let destination: Destination
    let sourceID: String?
    let ruleID: String?
    let reason: String
  }
  struct EditError: Error {
    let message: String
    init(_ message: String) { self.message = message }
  }

}
