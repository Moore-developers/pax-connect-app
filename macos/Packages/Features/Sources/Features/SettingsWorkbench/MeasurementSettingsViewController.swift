import AppKit
import Observation
import PaxCore
import PaxUI

/// Production measurement settings are saved by the daemon, not the demo store.
@MainActor
final class MeasurementSettingsViewController: NSViewController {
  private let routes: RoutesStore
  private var baseline: MeasurementPreferences?
  private var task: Task<Void, Never>?
  private let latencyMode = PaxRoutePicker()
  private let exitMode = PaxRoutePicker()
  private let downloadMode = PaxRoutePicker()
  private let latency = PaxRouteField("32")
  private let exit = PaxRouteField("8")
  private let download = PaxRouteField("8")
  private let status = NSTextField(wrappingLabelWithString: "正在读取测量配置…")
  private lazy var saveButton = PaxRouteButton(
    "保存测量配置", style: .primary, target: self, action: #selector(savePreferences))
  private lazy var resetButton = PaxRouteButton(
    "恢复默认", target: self, action: #selector(resetPreferences))
  private lazy var retryButton = PaxRouteButton(
    "重试", style: .quiet, target: self, action: #selector(reloadPreferences))
  init(routes: RoutesStore) {
    self.routes = routes
    super.init(nibName: nil, bundle: nil)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    for mode in [latencyMode, downloadMode] {
      mode.addItems(withTitles: ["使用默认值", "手动配置", "自动推荐"])
      mode.widthAnchor.constraint(equalToConstant: PaxTheme.Settings.pickerWidth).isActive = true
      mode.target = self
      mode.action = #selector(modeChanged)
    }
    exitMode.addItems(withTitles: ["自动调度（上限8）", "手动上限"])
    exitMode.widthAnchor.constraint(equalToConstant: PaxTheme.Settings.pickerWidth).isActive = true
    exitMode.target = self
    exitMode.action = #selector(modeChanged)
    let entries: [(String, String, NSPopUpButton, NSTextField)] = [
      ("HTTPS 延迟", "并发 1–100；默认按本机硬件确定。", latencyMode, latency),
      ("出口 IP", "HTTP请求上限1–32；备用与补地区也计数。", exitMode, exit),
      ("下载速度", "并发 1–8；默认 8，每批完成后自动继续。", downloadMode, download),
    ]
    let rows = entries.map { title, detail, mode, field in
      field.widthAnchor.constraint(equalToConstant: PaxTheme.Settings.numberWidth).isActive = true
      field.setAccessibilityLabel(title + "并发数")
      let controls = NSStackView(views: [mode, field])
      controls.spacing = PaxTheme.Spacing.tight
      controls.widthAnchor.constraint(
        equalToConstant: PaxTheme.Settings.pickerWidth + PaxTheme.Settings.numberWidth
          + PaxTheme.Spacing.tight
      ).isActive = true
      return PaxPreferenceRow(title: title, detail: detail, control: controls, style: .compact)
    }
    latency.setAccessibilityIdentifier("measurement-latency-parallel")
    exit.setAccessibilityIdentifier("measurement-exit-parallel")
    download.setAccessibilityIdentifier("measurement-download-parallel")
    latencyMode.setAccessibilityLabel("HTTPS 延迟模式")
    exitMode.setAccessibilityLabel("出口 IP 模式")
    downloadMode.setAccessibilityLabel("下载速度模式")
    saveButton.setAccessibilityIdentifier("measurement-save")
    resetButton.setAccessibilityIdentifier("measurement-reset")
    retryButton.setAccessibilityIdentifier("measurement-retry")
    retryButton.isHidden = true
    status.font = PaxTheme.Typography.caption()
    status.textColor = PaxTheme.Color.textSecondary
    status.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    let actions = NSStackView(views: [status, retryButton, NSView(), resetButton, saveButton])
    actions.spacing = PaxTheme.Spacing.tight
    view = PaxPreferenceGroup(
      title: "测试并发", symbol: "speedometer", rows: rows, style: .compact, footer: actions)
    reloadPreferences()
    observe()
  }
  private func observe() {
    withObservationTracking {
      updateAvailability()
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observe() }
    }
  }
  @objc private func modeChanged() { updateAvailability() }
  private func updateAvailability() {
    let available = routes.connected && baseline != nil && task == nil
    for picker in [latencyMode, downloadMode] { picker.isEnabled = available }
    latency.isEnabled = available && latencyMode.indexOfSelectedItem == 1
    download.isEnabled = available && downloadMode.indexOfSelectedItem == 1
    let exitSupported = routes.capabilities.contains("measurement.exit_pool.v1")
    exitMode.isEnabled = available && exitSupported
    exit.isEnabled = available && exitSupported && exitMode.indexOfSelectedItem == 1
    if exitSupported && exitMode.indexOfSelectedItem == 0 { exit.stringValue = "8" }
    saveButton.isEnabled = available
    resetButton.isEnabled = available
    retryButton.isEnabled = routes.connected && task == nil
  }
  @objc private func reloadPreferences() {
    guard task == nil else { return }
    retryButton.isHidden = true
    task = Task { [weak self, routes] in
      do {
        if case .preferences(let value) = try await routes.measurement(.preferences) {
          self?.baseline = value
          self?.render(value)
          self?.status.stringValue = "出口上限影响待发请求；延迟和下载配置下批生效。"
        }
      } catch {
        self?.status.stringValue = "读取失败：" + ProductErrorPresentation.display(error)
        self?.retryButton.isHidden = false
      }
      self?.task = nil
      self?.updateAvailability()
    }
    updateAvailability()
  }
  private func render(_ value: MeasurementPreferences) {
    latency.stringValue = String(value.latency)
    exit.stringValue = String(value.exit)
    download.stringValue = String(value.download)
    for (picker, value) in [
      (latencyMode, value.latencyMode), (downloadMode, value.downloadMode),
    ] { picker.selectItem(at: value == "manual" ? 1 : value == "auto" ? 2 : 0) }
    exitMode.selectItem(at: value.exitMode == "manual" ? 1 : 0)
    if routes.capabilities.contains("measurement.exit_pool.v1"), value.exitMode != "manual" {
      exit.stringValue = "8"
    }
    updateAvailability()
  }
  @objc private func savePreferences() {
    guard task == nil, var value = baseline, let l = UInt32(latency.stringValue),
      let e = UInt32(exit.stringValue), let d = UInt32(download.stringValue), (1...100).contains(l),
      (1...32).contains(e), (1...8).contains(d)
    else {
      status.stringValue = "请填写有效范围内的整数"
      return
    }
    value.latency = l
    value.exit = e
    value.download = d
    value.mode = "manual"
    value.latencyMode = ["default", "manual", "auto"][max(0, latencyMode.indexOfSelectedItem)]
    if routes.capabilities.contains("measurement.exit_pool.v1") {
      value.exitMode = exitMode.indexOfSelectedItem == 1 ? "manual" : "auto"
    }
    value.downloadMode = ["default", "manual", "auto"][max(0, downloadMode.indexOfSelectedItem)]
    task = Task { [weak self, routes] in
      do {
        if case .preferences(let saved) = try await routes.measurement(.savePreferences(value)) {
          self?.baseline = saved
          self?.render(saved)
          self?.status.stringValue =
            routes.capabilities.contains("measurement.exit_pool.v1")
            ? "已保存；出口影响待发请求，延迟和下载下批生效" : "已保存，下批任务生效"
        }
      } catch { self?.status.stringValue = ProductErrorPresentation.display(error) }
      self?.task = nil
      self?.updateAvailability()
    }
    updateAvailability()
  }
  @objc private func resetPreferences() {
    guard task == nil, let baseline else { return }
    latency.stringValue = String(baseline.latency)
    exit.stringValue = String(baseline.exit)
    download.stringValue = String(baseline.download)
    for picker in [latencyMode, exitMode, downloadMode] { picker.selectItem(at: 0) }
    if routes.capabilities.contains("measurement.exit_pool.v1") { exit.stringValue = "8" }
    savePreferences()
  }
}
