import Foundation
import PaxCore

/// Production configuration is confirmed by the daemon before changing visible rows.
@MainActor
extension RoutingWorkbenchStore {
  func stopLibraryRead() {
    guard !libraryBusy else { return }
    libraryTask?.cancel()
    libraryTask = nil
  }
  func routingCandidates() async throws -> RoutingProcessCandidates {
    if let telemetry { return try await telemetry.routingProcesses() }
    let appIDs = Set(applications.map(\.id))
    var applications: [String: UInt64] = [:]
    var processes: [String: UInt64] = [:]
    for connection in connections {
      let bytes = connection.transfers.reduce(UInt64(0)) { $0 + $1.upload + $1.download }
      guard bytes > 0 else { continue }
      processes[connection.ruleProcessPath, default: 0] += bytes
      if let id = connection.applicationID, appIDs.contains(id) {
        applications[id, default: 0] += bytes
      }
    }
    func ranked(_ values: [String: UInt64]) -> [RoutingCandidateTraffic] {
      values.sorted { a, b in
        a.value == b.value ? a.key < b.key : a.value > b.value
      }.map { .init(id: $0.key, bytes: $0.value) }
    }
    return RoutingProcessCandidates(
      applications: ranked(applications), processes: ranked(processes))
  }
  func reloadLibrary() {
    guard let telemetry, !libraryBusy else { return }
    libraryTask?.cancel()
    libraryTask = Task { [weak self] in
      do {
        let snapshot = try await telemetry.routingLibrary()
        guard let self, !Task.isCancelled else { return }
        applyLibrary(snapshot)
      } catch {
        if !Task.isCancelled {
          self?.setPackageMessage("rules_read.failed", failure: ProductFailure(error))
        }
      }
    }
  }
  func applyLibrary(_ value: RoutingLibrarySnapshot) {
    libraryRevision = value.revision
    libraryRoutes = value.routes
    libraryDomains = Dictionary(
      uniqueKeysWithValues: value.applications.map { ($0.id, $0.domains) })
    applications = value.applications.map { Application(id: $0.id, name: $0.name, helpers: []) }
    packages = value.packages.compactMap { p in
      guard let kind = Self.packageKind(p.kind) else { return nil }
      return RulePackage(
        id: p.id, name: p.name, kind: kind, destination: Self.destination(p.destination),
        enabled: p.enabled,
        rules: p.rules.compactMap { r in
          guard let method = Self.ruleMethod(r.method) else { return nil }
          return Rule(
            id: r.id, method: method, target: r.target,
            destination: Self.destination(r.destination), enabled: r.enabled)
        }, source: p.source, version: p.version,
        previousVersion: p.previousVersion.isEmpty ? nil : p.previousVersion,
        automatic: p.automatic, updateMessage: p.updateMessage, updateText: p.updateText,
        updatedAt: p.updatedAt
      )
    }
    if !packages.contains(where: { $0.id == selectedPackageID }) { selectedPackageID = "priority" }
    packageMessage = value.message
    packageMessageText = value.messageText
  }
  func commitLibrary(_ command: RoutingMutation) async throws {
    guard let telemetry, canEdit else { throw EditError(code: "routing_copy.693b856d9d") }
    var q = command
    q.expectedRevision = libraryRevision
    libraryBusy = true
    setPackageMessage(
      ["update", "update_geo"].contains(q.action)
        ? "routing_copy.dab833ac17" : "routing_copy.6bdb443509")
    defer { libraryBusy = false }
    do {
      let value = try await telemetry.mutateRoutingLibrary(q)
      applyLibrary(value)
    } catch {
      let conflict = (error as? ProductError)?.code == "routing_revision_conflict"
      let uncertain =
        (error as? ClientError).map { [.timeout, .unavailable, .unknown].contains($0) } ?? false
      if conflict || uncertain {
        do {
          applyLibrary(try await telemetry.routingLibrary())
          setPackageMessage(conflict ? "routing_copy.fe68ee114e" : "routing_copy.44301f82fa")
        } catch { setPackageMessage("routing_copy.c66cc13140") }
      } else {
        setPackageMessage("rules_read.operation_failed", failure: ProductFailure(error))
      }

      throw EditError(readMessage: libraryReadMessage)
    }
  }
  func performLibraryIntent(_ intent: Intent) -> Bool {
    var q = RoutingMutation()
    switch intent {
    case .movePackage(let id, let delta):
      q.action = "move"
      q.packageID = id
      q.delta = Int32(delta)
    case .enablePackage(let id, let value):
      q.action = "enable_package"
      q.packageID = id
      q.enabled = value
    case .packageDestination(let id, let value):
      q.action = "destination"
      q.packageID = id
      q.destination = Self.destinationCode(value)
    case .removeRule(let pack, let rule):
      q.action = "delete_rule"
      q.packageID = pack
      q.ruleID = rule
    case .removePackage(let id):
      q.action = "delete_package"
      q.packageID = id
    case .enableRule(let pack, let rule, let value):
      q.action = "enable_rule"
      q.packageID = pack
      q.ruleID = rule
      q.enabled = value
    case .rollback(let id):
      q.action = "rollback"
      q.packageID = id
    case .automatic(let id, let value):
      q.action = "automatic"
      q.packageID = id
      q.enabled = value
    default: return false
    }
    guard canEdit else { return true }
    libraryTask = Task { [weak self] in try? await self?.commitLibrary(q) }
    return true
  }
  func saveLibrary(_ intent: EditIntent) async throws {
    guard telemetry != nil else {
      try perform(intent)
      return
    }
    var q = RoutingMutation()
    let selected: String
    switch intent {
    case .package(let draft):
      q.action = "save_package"
      var p = RoutingPackage()
      p.id = draft.id ?? UUID().uuidString
      p.name = draft.name
      p.kind = draft.kind == .subscription ? "subscription" : "local"
      p.destination = Self.destinationCode(draft.destination)
      p.source = draft.source
      q.package = p
      selected = p.id
    case .rule(let draft):
      guard let pack = package(draft.packageID) else {
        throw EditError(code: "routing_copy.b3dcfffa77")
      }
      let projected = try ruleProjection(draft, in: pack)
      q.action = "replace_rules"
      q.packageID = pack.id
      q.rules = projected.0.rules.map { rule in
        var r = RoutingEntry()
        r.id = rule.id
        r.method = Self.methodCode(rule.method)
        r.target = rule.target
        r.destination = Self.destinationCode(rule.destination)
        r.enabled = rule.enabled
        return r
      }
      selected = pack.id
    }
    try await commitLibrary(q)
    selectedPackageID = selected
  }
  private static func packageKind(_ code: String) -> PackageKind? {
    switch code {
    case "priority": .priority
    case "local": .local
    case "subscription": .subscription
    case "geo": .geo
    case "adblock": .adblock
    case "claude": .claude
    default: nil
    }
  }
  private static func ruleMethod(_ code: String) -> Method? {
    switch code {
    case "application": .application
    case "process": .process
    case "domain": .domain
    case "domain_suffix": .domainSuffix
    case "ip": .ip
    case "cidr": .cidr
    case "builtin": .builtin
    case "source": .source
    default: nil
    }
  }
  private static func methodCode(_ method: Method) -> String {
    switch method {
    case .application: "application"
    case .process: "process"
    case .domain: "domain"
    case .domainSuffix: "domain_suffix"
    case .ip: "ip"
    case .cidr: "cidr"
    case .builtin: "builtin"
    case .source: "source"
    }
  }
  private static func destination(_ code: String) -> Destination {
    switch code {
    case "default": .followDefault
    case "global": .global
    case "direct": .direct
    case "block": .block
    default: .route(String(code.dropFirst(6)))
    }
  }
  private static func destinationCode(_ value: Destination) -> String {
    switch value {
    case .followDefault: "default"
    case .global: "global"
    case .direct: "direct"
    case .block: "block"
    case .route(let id): "route:" + id
    }
  }
}
