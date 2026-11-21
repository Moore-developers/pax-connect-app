import Foundation
import PaxCore

extension RoutingWorkbenchStore {
  enum PackageKind: String, CaseIterable {
    case priority = "优先规则"
    case local = "自己维护"
    case subscription = "从链接订阅"
    case geo = "内置"
    case adblock = "内置广告拦截"
    case claude = "内置 Claude"

    func label(context: PresentationContext) -> String {
      let names: [Self: String] = [
        .priority: "priority", .local: "local", .subscription: "subscription", .geo: "geo",
        .adblock: "adblock", .claude: "claude",
      ]
      return context.text("rules.kind." + (names[self] ?? "local"))
    }
    var isBuiltIn: Bool { self == .geo || self == .adblock || self == .claude }
    var canChangeDestination: Bool { self == .local || self == .subscription || self == .claude }
    var canUpdate: Bool { self == .subscription || self == .geo || self == .adblock }
  }
  enum Method: String, CaseIterable {
    case application = "应用"
    case process = "进程"
    case domain = "精确域名"
    case domainSuffix = "域名及子域"
    case ip = "单 IP"
    case cidr = "IP 网段"
    case builtin = "内置分类"
    case source = "订阅条件"
    func label(context: PresentationContext) -> String {
      let keys: [Self: String] = [
        .application: "application", .process: "process", .domain: "domain",
        .domainSuffix: "domain_suffix", .ip: "ip", .cidr: "cidr", .builtin: "builtin",
        .source: "source",
      ]
      return context.text("rules.method." + (keys[self] ?? "source"))
    }
  }
  // Raw values remain the existing stored rule identity; labels are display only.
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
    var updateText: ProductText? = nil
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
    private let meaning: ProductText?
    private let readMessage: RetainedProductMessage?
    init(_ message: String) {
      self.message = message
      meaning = nil
      readMessage = nil
    }
    init(code: String, params: [String: String] = [:]) {
      readMessage = nil
      meaning = ProductText(code: code, params: params)
      message = PresentationContext.chinese.text(code, params)
    }
    init(readMessage: RetainedProductMessage) {
      self.readMessage = readMessage
      message = readMessage.original
      meaning = nil
    }
    func message(context: PresentationContext) -> String {
      if let readMessage { return readMessage.text(context: context) }
      return ProductTextPresentation.text(meaning, original: message, context: context)
    }
  }

}

extension RoutingWorkbenchStore.Connection {
  /// Demo facts use short names. Their synthetic paths stay within the rule preview.
  var ruleProcessPath: String { process.hasPrefix("/") ? process : "/demo/" + process }
}
