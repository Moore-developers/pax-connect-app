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
  public var actionTitle: String {
    switch action {
    case .authorize: "授权连接"
    case .service, .conflict: "查看"
    case .retryConnection, .retrySelection: "重试"
    case .recheckCurrentPath: "重新验证"
    case .recoverBackend: "恢复后台"
    case .details: "查看原因"
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
  public var proxyProgressText: String {
    if degradedReason?.code == "pax_cleanup_pending" { return "正在清理旧 Pax 连接…" }
    if degradedReason?.code == "tun_releasing" { return "正在关闭其它 TUN…" }
    if degradedReason?.code == "network_recovering" {
      let name = currentRoute?.name ?? routesStore?.selectedRoute?.name
      return name.map { "网络恢复中 · \($0)" } ?? "网络恢复中…"
    }
    let target =
      !pendingTargetRouteID.isEmpty
      ? pendingTargetRouteID : routesStore?.selecting == true ? routesStore?.selectionTargetID : nil
    if let target, !target.isEmpty {
      if let name = routes.first(where: { $0.id == target })?.name, !name.isEmpty {
        return "正在切换到：\(name)…"
      }
      return "正在切换线路…"
    }
    if proxyEnabled {
      let name = currentRoute?.name ?? routesStore?.selectedRoute?.name
      if let name, !name.isEmpty { return "正在连接：\(name)…" }
      return "正在连接…"
    }
    return "正在恢复直连…"
  }

  public var feedback: [ConnectionFeedback] {
    let visible = unresolvedFeedback.filter { issue in
      dismissedFeedback[issue.id] != issue
        || dismissedFeedbackAttempt[issue.id] != routesStore?.feedbackOrder[issue.id]
    }
    return feedbackTimeline.update(visible, attempts: routesStore?.feedbackOrder ?? [:])
  }

  public var unresolvedFeedback: [ConnectionFeedback] {
    var items: [ConnectionFeedback] = []
    if connection != .connected {
      items.append(
        ConnectionFeedback(
          id: "connection", title: "后台连接已断开",
          detail: (clientError.map { $0.message + "。" } ?? "")
            + "保留最后一次数据，当前网络状态待确认。",
          action: requestBackendRecovery == nil ? .details : .recoverBackend))
    } else if needsServiceAuthorization {
      items.append(
        ConnectionFeedback(
          id: "authorization", title: "需要系统授权",
          detail: "授权代理内核后继续连接。密码仅用于本次 sudo 授权，不会保存。", action: .authorize))
    } else if let error = operationError {
      items.append(proxyFeedback(error, id: "proxy", retry: .retryConnection))
    }
    if let error = routesStore?.selectionError {
      let action =
        routesStore?.selectionTargetID.map(ConnectionFeedback.Action.retrySelection) ?? .details
      items.append(proxyFeedback(error, id: "selection", retry: action))
    }
    if status == .off, let error = degradedReason,
      ["system_proxy_taken_over", "mixed_unavailable"].contains(error.code)
    {
      items.append(
        ConnectionFeedback(
          id: "ingress", title: "Pax 已停止接管",
          detail: ProductErrorPresentation.message(error), action: .retryConnection))
    }
    if externalTUNActive {
      items.append(
        ConnectionFeedback(
          id: "external-tun",
          title: degradedReason?.code == "tun_taken_over" ? "其它应用已接管，Pax 已停止" : "其它 TUN 正在接管流量",
          detail: "请在对应应用或系统设置的 VPN 中手动关闭 TUN，再重试连接。", action: .details))
    }
    if let error = degradedReason, status == .degraded, !connectionTransitioning,
      error.code != "protection_unverified" || !currentPathSupported
    {
      items.append(
        ConnectionFeedback(
          id: "degraded", title: "连接需要注意",
          detail: ProductErrorPresentation.message(error),
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
            id: "degraded:" + reason.code, title: "连接需要注意",
            detail: ProductErrorPresentation.message(reason), action: .details))
      }
    }
    if currentPathSupported && currentPathCheck.state == "failed" {
      items.append(
        ConnectionFeedback(
          id: "current-path", title: "当前通道需要注意",
          detail: ProductErrorPresentation.message(.init(code: currentPathCheck.cause)),
          action: .recheckCurrentPath))
    }
    if powerForwardingFailed {
      items.append(
        ConnectionFeedback(
          id: "power", title: "睡眠状态同步中",
          detail: "睡眠或唤醒通知尚未确认送达，正在重试。", action: .details))
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
            id: issue.id, title: source.name + " · 更新失败", detail: issue.detail, action: .details)
        })
    for source in routesStore?.subscriptions ?? [] {
      if items.contains(where: { $0.id == "subscription:" + source.id }) { continue }
      if let error = routesStore?.refreshFailures[source.id],
        routesStore?.isRefreshing(source.id) != true
      {
        items.append(
          ConnectionFeedback(
            id: "subscription:" + source.id,
            title: source.name + " · 更新失败", detail: ProductErrorPresentation.message(error),
            action: .details))
      }
    }
    if let error = routesStore?.measurementError, !items.contains(where: { $0.id == "measurement" })
    {
      items.append(
        ConnectionFeedback(
          id: "measurement", title: "线路测量未完成",
          detail: ProductErrorPresentation.message(error), action: .details))
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

  private func proxyFeedback(_ error: ProductError, id: String, retry: ConnectionFeedback.Action)
    -> ConnectionFeedback
  {
    let title: String
    let action: ConnectionFeedback.Action
    switch error.code {
    case "core_required":
      title = "启动代理需要系统授权"
      action = .authorize
    case "service_privileges_required":
      title = "后台缺少系统权限"
      action = .service
    case "tun_conflict_other", "pax_tun_stale":
      title = "TUN 正在被占用"
      action = .conflict
    default:
      title = id == "selection" ? "线路切换失败" : "连接失败"
      action = retry
    }
    return ConnectionFeedback(
      id: id, title: title,
      detail: error.code == "service_privileges_required"
        ? "启动代理需要具备系统权限的代理内核；线路管理、订阅与测速可正常使用。"
        : ProductErrorPresentation.message(error), action: action)
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
