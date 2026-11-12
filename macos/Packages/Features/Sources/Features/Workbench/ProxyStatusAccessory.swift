import AppKit
import Observation
import PaxCore
import PaxUI

/// One feedback surface for both proxy switches, with actions owned by each problem.
@MainActor
final class ProxyStatusAccessory: NSView {
  private let connection: ConnectionStore?
  private weak var presenter: NSViewController?
  private let mode: NSView
  private let status = PaxInlineStatus()
  private let popover = NSPopover()
  private var primary: ConnectionFeedback?
  private var previousStatus: ProtectionStatus?
  private var successVisible = false
  private var successTask: Task<Void, Never>?
  init(mode: NSView, connection: ConnectionStore?, presenter: NSViewController) {
    self.mode = mode
    self.connection = connection
    self.presenter = presenter
    super.init(frame: .zero)
    addSubview(status)
    addSubview(mode)
    status.isHidden = connection == nil
    status.onAction = { [weak self] in self?.performStatusAction() }
    status.onMore = { [weak self] in self?.showProblems() }
    status.onDismiss = { [weak self] in
      guard let self else { return }
      connection?.dismissAllFeedback()
      popover.close()
    }
    popover.behavior = .transient
    observe()
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override var intrinsicContentSize: NSSize {
    let size = mode.intrinsicContentSize
    return NSSize(
      width: size.width
        + (connection == nil ? 0 : status.intrinsicContentSize.width + PaxTheme.Spacing.tight),
      height: max(size.height, status.intrinsicContentSize.height))
  }
  override func layout() {
    super.layout()
    let size = mode.intrinsicContentSize
    mode.frame = NSRect(
      x: max(0, bounds.width - size.width), y: (bounds.height - size.height) / 2,
      width: size.width, height: size.height)
    status.frame = NSRect(
      x: 0, y: 0, width: max(0, mode.frame.minX - PaxTheme.Spacing.tight), height: bounds.height)
  }
  private func observe() {
    guard let connection else { return }
    withObservationTracking {
      if previousStatus != connection.status {
        successTask?.cancel()
        successVisible = previousStatus != nil && connection.status == .protected
        previousStatus = connection.status
        if successVisible {
          successTask = Task { @MainActor [weak self] in
            do { try await Task.sleep(for: .seconds(PaxTheme.InlineStatus.successDuration)) } catch
            { return }
            self?.successVisible = false
            self?.observe()
          }
        }
      }
      let routes = connection.routesStore
      let busy =
        connection.requesting || (connection.proxyEnabled && connection.pendingOperationID != nil)
        || connection.proxyStarting || connection.connectionTransitioning
        || routes?.selecting == true
      let issues = connection.feedback
      primary = busy ? nil : issues.first
      if connection.connection != .connected {
        primary = issues.first { $0.id == "connection" }
        status.setVisible(true)
        let working =
          connection.backendRecoveryInProgress
          || [.connecting, .retrying].contains(connection.connection)
        status.apply(
          working ? .working : .failure,
          text: working ? "正在重连后台…" : "后台连接已断开",
          action: connection.canRecoverBackend ? "恢复后台" : nil,
          additionalCount: issues.count, detail: primary?.detail)
      } else if busy {
        status.setVisible(true)
        status.apply(
          .working,
          text: connection.proxyProgressText,
          action: connection.proxyStarting || connection.connectionTransitioning ? "取消" : nil,
          additionalCount: issues.count)
      } else if let primary {
        status.setVisible(true)
        status.apply(
          primary.action == .authorize || primary.id == "degraded" ? .warning : .failure,
          text: primary.title, action: "查看 \(issues.count) 项",
          dismissible: true, detail: primary.detail)
      } else if connection.proxyEnabled && connection.currentPathSupported
        && !connection.currentPathSummary.isEmpty
      {
        status.setVisible(true)
        status.apply(
          ["checking", "waiting_network"].contains(connection.currentPathCheck.state)
            ? .working : connection.currentPathCheck.state == "passed" ? .success : .idle,
          text: connection.currentPathSummary,
          action: connection.canCheckCurrentPath ? "重新验证" : nil,
          detail: connection.currentPathVerificationTime
        )
      } else if successVisible {
        status.setVisible(true)
        status.apply(.success, text: "已连接")
      } else {
        status.setVisible(false)
      }
      if popover.isShown { updateProblems() }
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observe() }
    }
  }
  private func performStatusAction() {
    guard let connection, let presenter else { return }
    if connection.connection != .connected {
      connection.recoverBackend()
      return
    }
    if primary != nil {
      showProblems()
      return
    }
    if connection.canCheckCurrentPath {
      Task { await connection.recheckCurrentPath() }
      return
    }
    if connection.proxyStarting || connection.connectionTransitioning {
      ProxyTransitionPresenter.request(from: presenter, connection: connection, enabled: false)
    }
  }
  private func perform(_ issue: ConnectionFeedback) {
    guard let connection, let presenter, connection.feedback.contains(issue) else { return }
    switch issue.action {
    case .recoverBackend:
      popover.close()
      connection.recoverBackend()
    case .authorize:
      popover.close()
      ProxyTransitionPresenter.request(from: presenter, connection: connection, enabled: true)
    case .recheckCurrentPath:
      popover.close()
      Task { await connection.recheckCurrentPath() }
    case .retryConnection:
      popover.close()
      if connection.proxyEnabled, connection.canReconnect {
        Task { await connection.reconnectProxy() }
      } else if !connection.proxyEnabled {
        ProxyTransitionPresenter.request(from: presenter, connection: connection, enabled: true)
      }
    case .retrySelection(let id):
      popover.close()
      Task { await connection.selectRoute(id) }
    case .conflict:
      popover.close()
      if let error = issue.id == "selection"
        ? connection.routesStore?.selectionError : connection.operationError
      {
        ProxyTransitionPresenter.presentConflict(
          from: presenter, connection: connection, error: error)
      }
    case .service, .details: showProblems()
    }
  }
  private func showProblems() {
    updateProblems()
    guard !popover.isShown, window != nil else { return }
    popover.applyMotionPreference()
    popover.show(relativeTo: status.bounds, of: status, preferredEdge: .minY)
  }
  private func updateProblems() {
    guard let connection else { return }
    let issues = connection.feedback
    guard !issues.isEmpty else {
      popover.close()
      return
    }
    let controller = NSViewController()
    let stack = NSStackView()
    stack.orientation = .vertical
    stack.alignment = .leading
    stack.spacing = PaxTheme.Spacing.regular
    stack.edgeInsets = NSEdgeInsets(
      top: PaxTheme.Spacing.regular, left: PaxTheme.Spacing.regular,
      bottom: PaxTheme.Spacing.regular, right: PaxTheme.Spacing.regular)
    for issue in issues {
      let title = NSTextField(wrappingLabelWithString: issue.title)
      title.font = PaxTheme.Typography.label()
      let detail = NSTextField(wrappingLabelWithString: issue.detail)
      detail.font = PaxTheme.Typography.caption()
      detail.textColor = PaxTheme.Color.textSecondary
      detail.preferredMaxLayoutWidth = PaxTheme.InlineStatus.detailsWidth
      stack.addArrangedSubview(title)
      stack.addArrangedSubview(detail)
      if issue.action != .details && issue.action != .service {
        let button = FeedbackActionButton(title: issue.actionTitle) { [weak self] in
          self?.perform(issue)
        }
        stack.addArrangedSubview(button)
      }
    }
    if let diagnostics = connection.trafficPathStore,
      connection.connection == .connected,
      connection.routesStore?.capabilities.contains("network.connection_diagnostics.v1") == true
    {
      let diagnose = FeedbackActionButton(title: "线路诊断…") { [weak self] in
        guard let self, let presenter = self.presenter else { return }
        self.popover.close()
        presenter.presentRouteModal(
          TrafficPathController(store: diagnostics, connection: self.connection))
      }
      diagnose.setAccessibilityIdentifier("proxy-feedback-diagnostics")
      stack.addArrangedSubview(diagnose)
    }
    if connection.canReconnect {
      let reconnect = FeedbackActionButton(title: "重新连接当前线路") { [weak self] in
        self?.popover.close()
        Task { await connection.reconnectProxy() }
      }
      reconnect.setAccessibilityIdentifier("proxy-reconnect")
      stack.addArrangedSubview(reconnect)
    }
    stack.widthAnchor.constraint(equalToConstant: PaxTheme.InlineStatus.detailsWidth).isActive =
      true
    let size = stack.fittingSize
    stack.frame = NSRect(origin: .zero, size: size)
    let scroll = NSScrollView(
      frame: NSRect(
        x: 0, y: 0, width: size.width,
        height: min(size.height, PaxTheme.InlineStatus.detailsMaximumHeight)))
    scroll.drawsBackground = false
    scroll.hasVerticalScroller = true
    scroll.autohidesScrollers = true
    scroll.documentView = stack
    controller.view = scroll
    popover.contentViewController = controller
    popover.contentSize = scroll.frame.size
    stack.scroll(NSPoint(x: 0, y: stack.isFlipped ? 0 : size.height))
  }
}

@MainActor
private final class FeedbackActionButton: NSButton {
  private let invoke: () -> Void
  init(title: String, invoke: @escaping () -> Void) {
    self.invoke = invoke
    super.init(frame: .zero)
    self.title = title
    isBordered = false
    contentTintColor = PaxTheme.Color.accent
    font = PaxTheme.Typography.caption()
    target = self
    action = #selector(performAction)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  @objc private func performAction() { invoke() }
}
