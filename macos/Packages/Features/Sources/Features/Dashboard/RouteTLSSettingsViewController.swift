import AppKit
import Observation
import PaxCore
import PaxUI

/// Confirmation binds to the exact route/revision shown; server rejects a stale sheet.
@MainActor
public final class RouteTLSSettingsViewController: NSViewController {
  let context: PresentationContext

  private let store: ConnectionStore
  var onContentResize: (() -> Void)?
  private var route: NodeProfile
  private let embedded: Bool
  private var observationRevision: UInt64 = 0
  private var dirty = false
  private var submitted = false
  private lazy var acknowledgement = NSButton(
    checkboxWithTitle: context.text("network_controls.ad09451ffe"), target: nil, action: nil)
  private lazy var warning = NSTextField(
    wrappingLabelWithString: context.text("network_controls.cde965afb6"))
  private let enableAfterSave: Bool
  private var awaitingEnable = false
  private var dismissed = false
  private var continuationTask: Task<Void, Never>?
  private let mode = NSPopUpButton(frame: .zero, pullsDown: false)
  private let summary = NSTextField(wrappingLabelWithString: "")
  private lazy var policyBadge = PaxBadge(title: context.text("network_controls.48905cc3c6"))
  private let policySource = PaxRouteText.label(
    "", font: PaxTheme.Typography.caption(), secondary: true)
  private lazy var serverField = RouteDetailField(
    context.text("network_controls.d3716cc5a2"), identifier: "tls-server")
  private lazy var sniField = RouteDetailField(
    context.text("network_controls.11acbb74e7"), identifier: "tls-sni")
  private let activeState = PaxRouteText.label(
    "", font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  private let resultLabel = NSTextField(wrappingLabelWithString: "")
  private lazy var save = PaxButton(
    .prominent, title: context.text("network_controls.c8550237ba"), target: nil, action: nil)
  private var task: Task<Void, Never>?
  public init(
    store: ConnectionStore, route: NodeProfile, enableAfterSave: Bool = false,
    embedded: Bool = false, context: PresentationContext = .chinese
  ) {
    self.context = context

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
    mode.addItems(withTitles: [
      context.text("network_controls.dfd7f42e89"), context.text("network_controls.299c0daaec"),
    ])
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
    if enableAfterSave { save.title = context.text("network_controls.e9b2433c5e") }
    save.target = self
    save.action = #selector(savePolicy)
    save.setAccessibilityIdentifier("save-tls-policy")
    let close = PaxButton(
      .secondary, title: context.text("network_controls.3fd47edce4"), target: self,
      action: #selector(closeSheet))
    let controls = NSStackView(views: [
      PaxRouteText.label(
        context.text("network_controls.7decdb6007"), font: PaxTheme.Typography.caption(),
        secondary: true),
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
    let source =
      node.tlsPolicy?.userOverride == true
      ? context.text("network_controls.7ce710f646") : context.text("network_controls.88250fa57c")
    let mode =
      node.tlsPolicy?.mode == .skipVerify
      ? context.text("network_controls.299c0daaec") : context.text("network_controls.48905cc3c6")
    let policy =
      source + " · " + mode
      + (node.tlsPolicy?.needsConfirmation == true
        ? context.text("network_controls.e09b637f7e") : "")
    return (embedded ? "" : node.name + "\n")
      + context.text(
        "network_controls.fc58c01d4b",
        [
          "arg0": String(describing: node.server), "arg1": String(describing: node.port),
          "arg2": String(describing: node.serverName),
        ]) + policy
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
      if embedded {
        save.title =
          skips
          ? context.text("network_controls.ca603f0ec8")
          : context.text("network_controls.8bceed7f77")
      }
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
          title: route.tlsPolicy?.mode == .skipVerify
            ? context.text("network_controls.7fccd23e40")
            : context.text("network_controls.f80009ff8f"),
          tone: route.tlsPolicy?.mode == .skipVerify ? .warning : .healthy)
        policySource.stringValue =
          (route.tlsPolicy?.userOverride == true
            ? context.text("network_controls.7ce710f646")
            : context.text("network_controls.88250fa57c"))
          + (route.tlsPolicy?.needsConfirmation == true
            ? context.text("network_controls.e09b637f7e") : "")
        serverField.value.stringValue = "\(route.server):\(route.port)"
        sniField.value.stringValue =
          route.serverName.isEmpty ? context.text("network_controls.83c6f641e8") : route.serverName
        if let active = store.activeTLS, active.routeID == route.id {
          activeState.stringValue =
            active.skipped
            ? context.text("network_controls.764df58d46")
            : (active.verified
              ? context.text("network_controls.2a080bef1f")
              : context.text("network_controls.a83baa7674"))
        } else if store.currentRoute?.id == route.id && store.status != .off {
          activeState.stringValue = context.text("network_controls.fa3d422e90")
        } else {
          activeState.stringValue = context.text("network_controls.d8e8b953ac")
        }
      }
      if store.tlsSaveRouteID == route.id, let error = store.tlsSaveError {
        resultLabel.stringValue = error
      } else if store.tlsSaveRouteID == route.id, let message = store.tlsSaveMessage {
        resultLabel.stringValue = message
      } else if !fresh {
        resultLabel.stringValue = context.text("network_controls.01a028e08f")
      } else if !embedded, let active = store.activeTLS, active.routeID == route.id {
        resultLabel.stringValue =
          active.skipped
          ? context.text("network_controls.05900eb8b2")
          : (active.verified
            ? context.text("network_controls.2a080bef1f")
            : context.text("network_controls.a83baa7674"))
      } else {
        resultLabel.stringValue =
          enableAfterSave
          ? context.text("network_controls.4914b9cfc2")
          : context.text("network_controls.9d04894908")
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
      alert.messageText = context.text("network_controls.aaf3b19bd9")
      alert.informativeText =
        context.text(
          "network_controls.34c6250ef7",
          [
            "arg0": String(describing: route.name), "arg1": String(describing: route.server),
            "arg2": String(describing: route.port), "arg3": String(describing: route.serverName),
            "arg4": String(
              describing: enableAfterSave
                ? context.text("network_controls.5633399fbc")
                : context.text("network_controls.ef7ce07385")),
          ])
      alert.addButton(withTitle: context.text("network_controls.2cd0f3be87"))
      alert.addButton(withTitle: context.text("network_controls.ca603f0ec8"))
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
