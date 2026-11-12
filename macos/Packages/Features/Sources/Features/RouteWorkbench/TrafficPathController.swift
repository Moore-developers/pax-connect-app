import AppKit
import Observation
import PaxCore
import PaxUI

/// One scrolling report. The reading position never changes detection or export scope.
@MainActor final class TrafficPathController: RouteModalController {
  private let store: TrafficPathStore
  private let connection: ConnectionStore?
  private var workbench: NetworkDiagnosticView?
  private let progressView = NetworkDiagnosticProgressView()
  private var refreshButton: PaxRouteButton?
  private var exportButton: PaxRouteButton?
  private var previewButton: PaxRouteButton?
  private var observedEnvironmentBoundary: String?
  private var observedBoundary: String?
  private var task: Task<Void, Never>?
  private var retainedOnClose = false

  init(store: TrafficPathStore, connection: ConnectionStore? = nil) {
    self.store = store
    self.connection = connection
    super.init(
      title: "线路诊断", subtitle: "检测当前 Mac 与浏览器的网络环境",
      size: PaxTheme.Routes.diagnosticWorkspace, style: .compact, symbol: "network")
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  override func loadView() {
    super.loadView()
    let refresh = button("开始检测", #selector(recheck), primary: true)
    refresh.setAccessibilityIdentifier("network-diagnostic-run")
    refreshButton = refresh
    pane.identityActions.insertArrangedSubview(refresh, at: 0)
    pane.header.addArrangedSubview(progressView)
    progressView.widthAnchor.constraint(equalTo: pane.header.widthAnchor).isActive = true
    progressView.onResize = { [weak self] in self?.pane.invalidateContentSize() }
    if let network = store.network {
      network.onlyDifferences = false
      let content = NetworkDiagnosticView(store: network, connection: connection)
      content.onResize = { [weak self] in self?.pane.invalidateContentSize() }
      workbench = content
      pane.add(content)
    } else {
      pane.add(PaxRouteText.label("当前连接不支持网络诊断。", wrapping: true))
    }
    let export = button("导出报告", #selector(exportReport))
    exportButton = export
    let preview = button("报告预览", #selector(previewReport))
    previewButton = preview
    pane.setActions([
      button("关闭", #selector(close)), preview,
      button("复制报告", #selector(copyReport)), export,
    ])
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
      ? "停止检测" : environment?.report == nil && network?.report == nil ? "开始检测" : "重新检测"
    refreshButton?.isEnabled = network != nil
    refreshButton?.isLoading = network?.busy == true || environment?.busy == true
    exportButton?.isEnabled =
      environment?.report != nil || network?.report != nil || store.report != nil
    previewButton?.title = network?.preview == true ? "返回诊断" : "报告预览"
    let messages = [network?.message, environment?.message].compactMap { $0 }
    let historyMessages = ["显示上次线路检测结果，请按需重新检测", "显示上次环境检测结果，请按需重新检测"]
    pane.status.stringValue =
      network?.historyMessage
      ?? (messages.allSatisfy { historyMessages.contains($0) }
        ? "显示上次检测结果，可重新检测更新" : messages.joined(separator: "；"))
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
  @objc private func previewReport() {
    guard let network = store.network else { return }
    network.preview.toggle()
    workbench?.apply()
    pane.layoutSubtreeIfNeeded()
    pane.bodyScroll.contentView.scroll(to: .zero)
  }
  private func reportText() -> String { workbench?.reportText() ?? "尚未检测" }
  @objc private func copyReport() {
    NSPasteboard.general.clearContents()
    NSPasteboard.general.setString(reportText(), forType: .string)
  }
  @objc private func exportReport() {
    guard let window = view.window else { return }
    let panel = NSSavePanel()
    panel.nameFieldStringValue = "Pax-线路诊断.md"
    let content = reportText()
    panel.beginSheetModal(for: window) { [weak self] result in
      guard result == .OK, let url = panel.url else { return }
      do { try content.write(to: url, atomically: true, encoding: .utf8) } catch {
        self?.showError(error)
      }
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
