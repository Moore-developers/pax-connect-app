import AppKit
import Observation
import PaxCore
import PaxUI

/// Confirmation binds to the exact route/revision shown; server rejects a stale sheet.
@MainActor
public final class RouteTLSSettingsViewController: NSViewController {
  private let store: ConnectionStore
  var onContentResize: (() -> Void)?
  private var route: NodeProfile
  private let embedded: Bool
  private var observationRevision: UInt64 = 0
  private var dirty = false
  private var submitted = false
  private let acknowledgement = NSButton(
    checkboxWithTitle: "我了解跳过后无法确认服务器身份", target: nil, action: nil)
  private let warning = NSTextField(wrappingLabelWithString: "跳过后连接仍加密，但无法确认服务器身份。仅在你信任此线路时使用。")
  private let enableAfterSave: Bool
  private var awaitingEnable = false
  private var dismissed = false
  private var continuationTask: Task<Void, Never>?
  private let mode = NSPopUpButton(frame: .zero, pullsDown: false)
  private let summary = NSTextField(wrappingLabelWithString: "")
  private let policyBadge = PaxBadge(title: "默认校验")
  private let policySource = PaxRouteText.label(
    "", font: PaxTheme.Typography.caption(), secondary: true)
  private let serverField = RouteDetailField("服务器地址", identifier: "tls-server")
  private let sniField = RouteDetailField("证书服务器名（SNI）", identifier: "tls-sni")
  private let activeState = PaxRouteText.label(
    "", font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  private let resultLabel = NSTextField(wrappingLabelWithString: "")
  private let save = PaxButton(.prominent, title: "保存设置", target: nil, action: nil)
  private var task: Task<Void, Never>?
  public init(
    store: ConnectionStore, route: NodeProfile, enableAfterSave: Bool = false,
    embedded: Bool = false
  ) {
    self.embedded = embedded
    self.store = store
    self.route = route
    self.enableAfterSave = enableAfterSave
    super.init(nibName: nil, bundle: nil)
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override func loadView() {
    view = NSView(frame: embedded ? .zero : NSRect(x: 0, y: 0, width: 540, height: 330))
    mode.addItems(withTitles: ["默认校验证书", "跳过校验"])
    mode.selectItem(at: route.tlsPolicy?.mode == .skipVerify ? 1 : 0)
    mode.setAccessibilityIdentifier("tls-mode")
    mode.target = self
    mode.action = #selector(modeChanged)
    acknowledgement.target = self
    acknowledgement.action = #selector(acknowledged)
    acknowledgement.setAccessibilityIdentifier("tls-skip-acknowledgement")
    acknowledgement.state =
      route.tlsPolicy?.mode == .skipVerify && route.tlsPolicy?.needsConfirmation == false
      ? .on : .off
    warning.textColor = PaxTheme.Color.warning
    warning.font = PaxTheme.Typography.caption()
    summary.stringValue = summaryText(route)
    if enableAfterSave { save.title = "保存并开启代理" }
    save.target = self
    save.action = #selector(savePolicy)
    save.setAccessibilityIdentifier("save-tls-policy")
    let close = PaxButton(.secondary, title: "关闭", target: self, action: #selector(closeSheet))
    let controls = NSStackView(views: [
      PaxRouteText.label("下次连接", font: PaxTheme.Typography.caption(), secondary: true),
      mode, NSView(), save,
    ])
    controls.setAccessibilityIdentifier("tls-controls")
    let policy = NSStackView(views: [policyBadge, policySource])
    policy.spacing = PaxTheme.Spacing.medium
    let fields = PaxFormGrid(rows: [[serverField, sniField]])
    fields.setAccessibilityIdentifier("tls-properties")
    activeState.setAccessibilityIdentifier("tls-active-state")
    controls.distribution = .fill
    controls.spacing = PaxTheme.Spacing.medium
    let stack = NSStackView(
      views: embedded
        ? [policy, fields, activeState, controls, warning, acknowledgement, resultLabel]
        : [summary, mode, resultLabel, save, close])
    if embedded {
      for child in [policy, fields, activeState, controls] {
        child.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
      }
    }
    if embedded {
      for label in [summary, warning, resultLabel] {
        label.font = PaxTheme.Typography.caption()
        label.textColor = label === warning ? PaxTheme.Color.warning : PaxTheme.Routes.muted
      }
    }
    stack.orientation = .vertical
    stack.alignment = .leading
    stack.spacing = embedded ? PaxTheme.Spacing.medium : PaxTheme.Spacing.roomy
    stack.translatesAutoresizingMaskIntoConstraints = false
    view.addSubview(stack)
    let inset: CGFloat = embedded ? 0 : PaxTheme.Spacing.roomy
    NSLayoutConstraint.activate([
      stack.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: inset),
      stack.trailingAnchor.constraint(
        equalTo: view.trailingAnchor, constant: -inset),
      stack.topAnchor.constraint(equalTo: view.topAnchor, constant: inset),
      stack.bottomAnchor.constraint(equalTo: view.bottomAnchor, constant: -inset),
    ])
    for label in (embedded ? [warning, resultLabel] : [summary, warning, resultLabel]) {
      label.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
    }
  }
  private func summaryText(_ node: NodeProfile) -> String {
    let source = node.tlsPolicy?.userOverride == true ? "用户设置" : "来源设置"
    let mode = node.tlsPolicy?.mode == .skipVerify ? "跳过校验" : "默认校验"
    let policy =
      source + " · " + mode + (node.tlsPolicy?.needsConfirmation == true ? " · 尚待确认" : "")
    return (embedded ? "" : node.name + "\n")
      + "服务器：\(node.server):\(node.port)\nSNI：\(node.serverName)\n" + policy
  }
  @objc private func modeChanged() {
    dirty = true
    acknowledgement.state = .off
    observe()
    onContentResize?()
  }
  @objc private func acknowledged() { observe() }
  public override func viewDidLoad() {
    super.viewDidLoad()
    observe()
  }
  private func observe() {
    observationRevision &+= 1
    let revision = observationRevision
    withObservationTracking {
      if awaitingEnable, !store.tlsSaving, store.pendingOperationID == nil,
        store.operationError == nil, store.tlsSaveMessage != nil,
        store.routesStore?.selectedRoute?.id == route.id,
        store.proxyConfirmationRoute == nil, store.canToggleProxy
      {
        awaitingEnable = false
        continuationTask = Task { [weak self, store, route] in
          guard let self, !self.dismissed, !Task.isCancelled,
            store.routesStore?.selectedRoute?.id == route.id,
            store.routesStore?.selectedRoute?.tlsPolicy?.identityToken
              == route.tlsPolicy?.identityToken,
            store.canToggleProxy
          else { return }
          let presenter = self.presentingViewController
          self.dismissed = true
          self.dismiss(self)
          await Task.yield()
          guard !Task.isCancelled, store.routesStore?.selectedRoute?.id == route.id,
            let presenter
          else { return }
          ProxyTransitionPresenter.request(from: presenter, connection: store, enabled: true)
        }
      }
      let current = store.routes.first { $0.id == route.id }
      if embedded, let current, !store.tlsSaving,
        !dirty
          || (submitted && store.tlsSaveRouteID == route.id && store.tlsSaveMessage != nil
            && store.tlsSaveError == nil)
      {
        route = current
        dirty = false
        submitted = false
        mode.selectItem(at: route.tlsPolicy?.mode == .skipVerify ? 1 : 0)
        acknowledgement.state =
          route.tlsPolicy?.mode == .skipVerify && route.tlsPolicy?.needsConfirmation == false
          ? .on : .off
      }
      let skips = mode.indexOfSelectedItem == 1
      warning.isHidden = !skips
      acknowledgement.isHidden = !skips
      if embedded { save.title = skips ? "确认跳过并保存" : "保存证书设置" }
      mode.isEnabled = store.canEditTLS
      acknowledgement.isEnabled = store.canEditTLS
      let fresh =
        current?.tlsPolicy?.identityToken == route.tlsPolicy?.identityToken
        && current?.tlsPolicy?.revision == route.tlsPolicy?.revision
      save.isEnabled =
        store.canEditTLS && fresh && (!embedded || !skips || acknowledgement.state == .on)
      let shown = current ?? route
      summary.stringValue = summaryText(shown)
      if embedded {
        policyBadge.apply(
          title: route.tlsPolicy?.mode == .skipVerify ? "已保存：跳过校验" : "已保存：默认校验",
          tone: route.tlsPolicy?.mode == .skipVerify ? .warning : .healthy)
        policySource.stringValue =
          (route.tlsPolicy?.userOverride == true ? "用户设置" : "来源设置")
          + (route.tlsPolicy?.needsConfirmation == true ? " · 尚待确认" : "")
        serverField.value.stringValue = "\(route.server):\(route.port)"
        sniField.value.stringValue = route.serverName.isEmpty ? "随服务器地址" : route.serverName
        if let active = store.activeTLS, active.routeID == route.id {
          activeState.stringValue =
            active.skipped
            ? "当前连接：跳过校验，未确认服务器身份"
            : (active.verified ? "当前连接：证书已校验" : "当前连接：默认校验证书")
        } else if store.currentRoute?.id == route.id && store.status != .off {
          activeState.stringValue = "当前连接未提供证书状态"
        } else {
          activeState.stringValue = "当前未通过此线路连接"
        }
      }
      if store.tlsSaveRouteID == route.id, let error = store.tlsSaveError {
        resultLabel.stringValue = error
      } else if store.tlsSaveRouteID == route.id, let message = store.tlsSaveMessage {
        resultLabel.stringValue = message
      } else if !fresh {
        resultLabel.stringValue = "线路设置已变化，请关闭后重新打开核对"
      } else if !embedded, let active = store.activeTLS, active.routeID == route.id {
        resultLabel.stringValue =
          active.skipped ? "当前连接：跳过校验，未校验服务器证书" : (active.verified ? "当前连接：证书已校验" : "当前连接：默认校验证书")
      } else {
        resultLabel.stringValue =
          enableAfterSave ? "保存成功后继续开启这条线路" : "保存后下次开启生效，不会自动开启或重连"
      }
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in
        guard let self, self.observationRevision == revision else { return }
        self.observe()
        self.onContentResize?()
      }
    }
  }
  @objc private func savePolicy() {
    guard task == nil, store.canEditTLS else { return }
    let selected: TLSMode = mode.indexOfSelectedItem == 1 ? .skipVerify : .strict
    if embedded {
      guard selected != .skipVerify || acknowledgement.state == .on else { return }
      submit(selected, confirmed: selected == .skipVerify)
      return
    }
    if selected == .skipVerify {
      let alert = NSAlert()
      alert.messageText = "仅对这条线路跳过证书校验？"
      alert.informativeText =
        "\(route.name)\n\(route.server):\(route.port) · SNI \(route.serverName)\n服务器身份将无法确认。连接仍加密，但显示降级。保存后\(enableAfterSave ? "继续开启代理。" : "下次连接生效。")"
      alert.addButton(withTitle: "取消")
      alert.addButton(withTitle: "确认跳过并保存")
      guard let window = view.window else { return }
      alert.beginSheetModal(for: window) { [weak self] response in
        Task { @MainActor [weak self] in
          guard response == .alertSecondButtonReturn else { return }
          self?.submit(.skipVerify, confirmed: true)
        }
      }
    } else {
      submit(.strict, confirmed: false)
    }
  }
  private func submit(_ mode: TLSMode, confirmed: Bool) {
    guard task == nil, store.canEditTLS else { return }
    submitted = true
    task = Task { [weak self, store, route] in
      await store.perform(
        .updateRouteTLS(RouteTLSIntent(route: route, mode: mode, confirmSkip: confirmed)))
      self?.awaitingEnable = !Task.isCancelled && self?.enableAfterSave == true
      self?.task = nil
      self?.observe()
    }
  }
  public override func cancelOperation(_ sender: Any?) { closeSheet() }
  @objc private func closeSheet() {
    dismissed = true
    awaitingEnable = false
    continuationTask?.cancel()
    task?.cancel()
    dismiss(self)
  }
}
