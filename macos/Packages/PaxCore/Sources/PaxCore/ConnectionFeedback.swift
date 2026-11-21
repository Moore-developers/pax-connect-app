import Foundation

/// A recoverable problem owns its action; subscription failures never reconnect the proxy.
public struct ConnectionFeedback: Equatable, Identifiable, Sendable {
  public enum Action: Equatable, Sendable {
    case authorize, service, conflict, retryConnection, recheckCurrentPath, recoverBackend
    case retrySelection(String)
    case details
  }
  public let id: String
  public let title: String
  public let detail: String
  public let action: Action
  public init(id: String, title: String, detail: String, action: Action) {
    self.id = id
    self.title = title
    self.detail = detail
    self.action = action
  }
  public var actionTitle: String { actionTitle(context: .chinese) }
  public func actionTitle(context: PresentationContext) -> String {
    switch action {
    case .authorize: context.text("network_controls.b423c50f29")
    case .service, .conflict: context.text("network_controls.db8db05304")
    case .retryConnection, .retrySelection: context.text("network_controls.b8784c8dd5")
    case .recheckCurrentPath: context.text("network_controls.73224d5fe5")
    case .recoverBackend: context.text("network_controls.2f13cbac4d")
    case .details: context.text("network_controls.856cba0533")
    }
  }
}

extension ConnectionStore {
  public var canRecoverBackend: Bool {
    connection != .connected && !backendRecoveryInProgress && requestBackendRecovery != nil
  }
  public func recoverBackend() {
    guard canRecoverBackend else { return }
    requestBackendRecovery?()
  }

  /// Prefer the daemon target; the in-flight local target fills gaps before
  /// that target is available. Never label a switch with the previous route.
  public var proxyProgressText: String { proxyProgressText(context: .chinese) }
  public func proxyProgressText(context: PresentationContext) -> String {
    if degradedReason?.code == "pax_cleanup_pending" {
      return context.text("network_controls.0200e69b74")
    }
    if degradedReason?.code == "tun_releasing" {
      return context.text("network_controls.ebf7e6dc11")
    }
    if degradedReason?.code == "network_recovering" {
      let name = currentRoute?.name ?? routesStore?.selectedRoute?.name
      return name.map {
        context.text("network_controls.a239fb5b1f", ["arg0": String(describing: $0)])
      } ?? context.text("network_controls.1d0751517f")
    }
    let target =
      !pendingTargetRouteID.isEmpty
      ? pendingTargetRouteID : routesStore?.selecting == true ? routesStore?.selectionTargetID : nil
    if let target, !target.isEmpty {
      if let name = routes.first(where: { $0.id == target })?.name, !name.isEmpty {
        return context.text("network_controls.c866469ad1", ["arg0": String(describing: name)])
      }
      return context.text("network_controls.de1102293b")
    }
    if proxyEnabled {
      let name = currentRoute?.name ?? routesStore?.selectedRoute?.name
      if let name, !name.isEmpty {
        return context.text("network_controls.8a46ba340c", ["arg0": String(describing: name)])
      }
      return context.text("network_controls.8be0ea36a9")
    }
    return context.text("network_controls.ebbe9d1fb8")
  }

  public var feedback: [ConnectionFeedback] {
    let visible = unresolvedFeedback.filter { issue in
      dismissedFeedback[issue.id] != issue
        || dismissedFeedbackAttempt[issue.id] != routesStore?.feedbackOrder[issue.id]
    }
    return feedbackTimeline.update(visible, attempts: routesStore?.feedbackOrder ?? [:])
  }

  public func feedback(context: PresentationContext) -> [ConnectionFeedback] {
    let localized = Dictionary(
      unresolvedFeedback(context: context).map { ($0.id, $0) }, uniquingKeysWith: { $1 })
    return feedback.map { localized[$0.id] ?? $0 }
  }
  public var unresolvedFeedback: [ConnectionFeedback] { unresolvedFeedback(context: .chinese) }
  public func unresolvedFeedback(context: PresentationContext) -> [ConnectionFeedback] {
    var items: [ConnectionFeedback] = []
    if connection != .connected {
      items.append(
        ConnectionFeedback(
          id: "connection", title: context.text("network_controls.d2d1752a0b"),
          detail: (clientError.map { ProductErrorPresentation.message($0, context: context) + "。" }
            ?? "")
            + context.text("network_controls.72fae1defb"),
          action: requestBackendRecovery == nil ? .details : .recoverBackend))
    } else if needsServiceAuthorization {
      items.append(
        ConnectionFeedback(
          id: "authorization", title: context.text("network_controls.a881831d4e"),
          detail: context.text("network_controls.45ea9a5046"), action: .authorize))
    } else if let error = operationError {
      items.append(proxyFeedback(error, id: "proxy", retry: .retryConnection, context: context))
    }
    if let error = routesStore?.selectionError {
      let action =
        routesStore?.selectionTargetID.map(ConnectionFeedback.Action.retrySelection) ?? .details
      items.append(proxyFeedback(error, id: "selection", retry: action, context: context))
    }
    if status == .off, let error = degradedReason,
      ["system_proxy_taken_over", "mixed_unavailable"].contains(error.code)
    {
      items.append(
        ConnectionFeedback(
          id: "ingress", title: context.text("network_controls.c12211abc3"),
          detail: ProductErrorPresentation.message(error, context: context),
          action: .retryConnection))
    }
    if externalTUNActive {
      items.append(
        ConnectionFeedback(
          id: "external-tun",
          title: degradedReason?.code == "tun_taken_over"
            ? context.text("network_controls.1b7407589d")
            : context.text("network_controls.eca3426b18"),
          detail: context.text("network_controls.b014e9b86b"), action: .details))
    }
    if let error = degradedReason, status == .degraded, !connectionTransitioning,
      error.code != "protection_unverified" || !currentPathSupported
    {
      items.append(
        ConnectionFeedback(
          id: "degraded", title: context.text("network_controls.6b8ba95e69"),
          detail: ProductErrorPresentation.message(error, context: context),
          action: [
            "route_lost", "tun_missing", "dns_unprotected", "proxy_unreachable",
            "route_connections_pending", "mixed_probe_failed", "mixed_unavailable",
            "system_proxy_write_failed", "system_proxy_observation_unavailable",
          ].contains(error.code) && canReconnect ? .retryConnection : .details))
    }
    if status == .degraded, !connectionTransitioning {
      var seen = Set([degradedReason?.code].compactMap { $0 })
      var additional = degradedReasons
      if activeTLS?.skipped == true { additional.append(.init(code: "tls_unverified")) }
      for reason in additional where !seen.contains(reason.code) {
        seen.insert(reason.code)
        if reason.code == "protection_unverified" && currentPathSupported { continue }
        items.append(
          ConnectionFeedback(
            id: "degraded:" + reason.code, title: context.text("network_controls.6b8ba95e69"),
            detail: ProductErrorPresentation.message(reason, context: context), action: .details))
      }
    }
    if currentPathSupported && currentPathCheck.state == "failed" {
      items.append(
        ConnectionFeedback(
          id: "current-path", title: context.text("network_controls.3cd70ae144"),
          detail: ProductErrorPresentation.message(
            .init(code: currentPathCheck.cause), context: context),
          action: .recheckCurrentPath))
    }
    if powerForwardingFailed {
      items.append(
        ConnectionFeedback(
          id: "power", title: context.text("network_controls.1311fd4b2e"),
          detail: context.text("network_controls.e3c43e45aa"), action: .details))
    }
    let priorityCount = items.count
    items.append(
      contentsOf: (routesStore?.actionFeedbacks.values ?? [String: ConnectionFeedback]().values)
        .sorted { $0.id < $1.id }.map { issue in
          guard issue.id.hasPrefix("subscription:"),
            let source = routesStore?.subscriptions.first(where: {
              "subscription:" + $0.id == issue.id
            })
          else { return issue }
          return ConnectionFeedback(
            id: issue.id, title: source.name + context.text("network_controls.6aae0cffaa"),
            detail: issue.detail, action: .details)
        })
    for source in routesStore?.subscriptions ?? [] {
      if items.contains(where: { $0.id == "subscription:" + source.id }) { continue }
      if let error = routesStore?.refreshFailures[source.id],
        routesStore?.isRefreshing(source.id) != true
      {
        items.append(
          ConnectionFeedback(
            id: "subscription:" + source.id,
            title: source.name + context.text("network_controls.6aae0cffaa"),
            detail: ProductErrorPresentation.message(error, context: context),
            action: .details))
      }
    }
    if let error = routesStore?.measurementError, !items.contains(where: { $0.id == "measurement" })
    {
      items.append(
        ConnectionFeedback(
          id: "measurement", title: context.text("network_controls.bd45a590b3"),
          detail: ProductErrorPresentation.message(error, context: context), action: .details))
    }
    return Array(items.prefix(priorityCount))
      + items.dropFirst(priorityCount).sorted {
        let left = routesStore?.feedbackOrder[$0.id] ?? 0
        let right = routesStore?.feedbackOrder[$1.id] ?? 0
        return left == right ? $0.id < $1.id : left > right
      }
  }

  public func dismissAllFeedback() {
    for issue in unresolvedFeedback { dismissFeedback(issue) }
  }

  public func dismissFeedback(_ issue: ConnectionFeedback) {
    // Suppress presentation only; the authoritative failure and connection state stay intact.
    dismissedFeedback[issue.id] = issue
    dismissedFeedbackAttempt[issue.id] = routesStore?.feedbackOrder[issue.id]
  }

  private func proxyFeedback(
    _ error: ProductError, id: String, retry: ConnectionFeedback.Action,
    context: PresentationContext = .chinese
  )
    -> ConnectionFeedback
  {
    let title: String
    let action: ConnectionFeedback.Action
    switch error.code {
    case "core_required":
      title = context.text("network_controls.68b971b4a5")
      action = .authorize
    case "service_privileges_required":
      title = context.text("network_controls.81169208d7")
      action = .service
    case "tun_conflict_other", "pax_tun_stale":
      title = context.text("network_controls.dca6b69388")
      action = .conflict
    default:
      title =
        id == "selection"
        ? context.text("network_controls.9e1b3d1cbe") : context.text("network_controls.89c4766afc")
      action = retry
    }
    return ConnectionFeedback(
      id: id, title: title,
      detail: error.code == "service_privileges_required"
        ? context.text("network_controls.fdcbf2e381")
        : ProductErrorPresentation.message(error, context: context), action: action)
  }
}

/// Session-local ordering follows observed arrivals/changes, not problem category priority.
struct FeedbackTimeline {
  private var previous: [String: ConnectionFeedback] = [:]
  private var attempts: [String: UInt64] = [:]
  private var order: [String: UInt64] = [:]
  private var sequence: UInt64 = 0
  mutating func update(_ issues: [ConnectionFeedback], attempts nextAttempts: [String: UInt64])
    -> [ConnectionFeedback]
  {
    let ids = Set(issues.map(\.id))
    previous = previous.filter { ids.contains($0.key) }
    order = order.filter { ids.contains($0.key) }
    for issue in issues.reversed() {
      if previous[issue.id] != issue || attempts[issue.id] != nextAttempts[issue.id] {
        sequence &+= 1
        order[issue.id] = sequence
      }
      previous[issue.id] = issue
    }
    attempts = nextAttempts.filter { ids.contains($0.key) }
    return issues.sorted { (order[$0.id] ?? 0) > (order[$1.id] ?? 0) }
  }
}
