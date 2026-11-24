import AppKit
import Observation
import PaxCore
import PaxUI

/// The diagnostic overview scrolls as one page in the presentation container.
@MainActor final class TrafficPathController: RouteModalController {
  override var localizedSubtitleKey: String? { "modal.network.environment_note" }
  override var localizedTitleKey: String? { "routes.diagnostics" }
  private let store: TrafficPathStore
  private let connection: ConnectionStore?
  private var workbench: NetworkDiagnosticView?
  private lazy var progressView = NetworkDiagnosticProgressView(context: interfaceCopy.context)
  private var refreshButton: PaxRouteButton?
  private var observedEnvironmentBoundary: String?
  private var observedBoundary: String?
  private var task: Task<Void, Never>?
  private var retainedOnClose = false

  init(store: TrafficPathStore, connection: ConnectionStore? = nil) {
    self.store = store
    self.connection = connection
    super.init(
      title: "线路诊断", subtitle: "检测当前 Mac 与浏览器的网络环境",
      size: PaxTheme.Routes.diagnosticWorkspace, style: .overview, symbol: "network")
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  override func loadView() {
    super.loadView()
    let refresh = button(
      self.interfaceCopy.text("action.diagnostic.start"), #selector(recheck), primary: true)
    refresh.setAccessibilityIdentifier("network-diagnostic-run")
    refreshButton = refresh
    pane.identityActions.insertArrangedSubview(refresh, at: 0)
    pane.header.addArrangedSubview(progressView)
    progressView.widthAnchor.constraint(equalTo: pane.header.widthAnchor).isActive = true
    progressView.onResize = { [weak self] in self?.pane.invalidateContentSize() }
    if let network = store.network {
      network.topic = 0
      let content = NetworkDiagnosticView(
        store: network, context: interfaceCopy.context)
      content.onResize = { [weak self] in self?.pane.invalidateContentSize() }
      workbench = content
      pane.add(content)
    } else {
      pane.add(self.interfaceCopy.routeLabel("action.diagnostic.unavailable", wrapping: true))
    }
    pane.setActions([button(self.interfaceCopy.text("common.close"), #selector(close))])
    observe()
    task = Task { [weak self, store] in
      await store.refresh()
      self?.task = nil
    }
  }

  private func observe() {
    withObservationTracking {
      render()
    } onChange: { [weak self] in
      Task { @MainActor in self?.observe() }
    }
  }
  private func render() {
    let network = store.network
    let environment = network?.currentEnvironment
    if let network { progressView.apply(network) }
    let environmentBoundary =
      "\(connection?.connectionGeneration ?? 0)|\(connection?.currentRoute?.id ?? "")|\(connection?.routesStore?.physicalEgress?.network ?? "")|\(connection?.connection == .connected)"
    if let old = observedEnvironmentBoundary, old != environmentBoundary {
      environment?.invalidate()
    }
    observedEnvironmentBoundary = environmentBoundary
    let selected = network?.report?.targetRouteID ?? store.report?.targetRouteID ?? ""
    let route = connection?.routesStore?.routes.first { $0.id == selected }
    let boundary =
      "\(connection?.connectionGeneration ?? 0)|\(connection?.currentRoute?.id ?? "")|\(connection?.routesStore?.physicalEgress?.network ?? "")|\(connection?.routesStore?.selectedRouteID ?? "")|\(route?.configRevision ?? 0)|\(connection?.connection == .connected)"
    if let old = observedBoundary, old != boundary { network?.invalidate() }
    observedBoundary = boundary
    refreshButton?.title =
      network?.busy == true || environment?.busy == true
      ? self.interfaceCopy.text("interface_detail.605fca0de8")
      : environment?.report == nil && network?.report == nil
        ? self.interfaceCopy.text("action.diagnostic.start")
        : self.interfaceCopy.text("interface_detail.a13550662f")
    refreshButton?.isEnabled = network != nil
    refreshButton?.isLoading = network?.busy == true || environment?.busy == true
    let messages = [
      network?.message(context: interfaceCopy.context),
      environment?.message(context: interfaceCopy.context),
    ].compactMap { $0 }
    let historical =
      (network?.isShowingHistoryMessage ?? true) && (environment?.isShowingHistoryMessage ?? true)
    pane.status.stringValue =
      network?.historyMessage(context: interfaceCopy.context)
      ?? (historical
        ? interfaceCopy.text("interface_detail.f4d97d29fd")
        : messages.joined(separator: interfaceCopy.context.language == .chinese ? "；" : "; "))
    pane.status.isHidden = false
    workbench?.apply()
  }
  @objc private func recheck() {
    guard let network = store.network else { return }
    if network.busy || network.currentEnvironment?.busy == true {
      network.cancel()
      network.currentEnvironment?.cancel()
    } else {
      network.startAll()
    }
  }
  private func clear() {
    guard !retainedOnClose else { return }
    retainedOnClose = true
    progressView.invalidate()
    task?.cancel()
    task = nil
    store.network?.retainLastResult()
    store.clear()
  }
  override func close() {
    clear()
    super.close()
  }
  override func viewDidDisappear() {
    super.viewDidDisappear()
    clear()
  }
}
