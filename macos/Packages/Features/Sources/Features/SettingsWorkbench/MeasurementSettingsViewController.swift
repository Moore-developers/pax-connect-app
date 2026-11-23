import AppKit
import Observation
import PaxCore
import PaxUI

/// Production measurement settings are saved by the daemon, not the demo store.
@MainActor
final class MeasurementSettingsViewController: NSViewController, InterfaceLanguageConsumer {
  let interfaceCopy = InterfaceCopyBindings()
  private var feedback: (String, [String: String])?
  private let routes: RoutesStore
  private var baseline: MeasurementPreferences?
  private var task: Task<Void, Never>?
  private let latencyMode = PaxRoutePicker()
  private let exitMode = PaxRoutePicker()
  private let downloadMode = PaxRoutePicker()
  private let latency = PaxRouteField("32")
  private let exit = PaxRouteField("8")
  private let download = PaxRouteField("8")
  private var group: PaxPreferenceGroup?
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
      interfaceCopy.picker(
        mode,
        keys: [
          "settings.measurement.default", "settings.measurement.manual",
          "settings.measurement.auto",
        ])
      mode.widthAnchor.constraint(equalToConstant: PaxTheme.Settings.pickerWidth).isActive = true
      mode.target = self
      mode.action = #selector(modeChanged)
    }
    interfaceCopy.picker(
      exitMode, keys: ["settings.measurement.exit_auto", "settings.measurement.exit_manual"])
    exitMode.widthAnchor.constraint(equalToConstant: PaxTheme.Settings.pickerWidth).isActive = true
    exitMode.target = self
    exitMode.action = #selector(modeChanged)
    let entries: [(String, String, NSPopUpButton, NSTextField)] = [
      ("settings.measurement.latency", "settings.measurement.latency_detail", latencyMode, latency),
      ("settings.measurement.exit", "settings.measurement.exit_detail", exitMode, exit),
      (
        "settings.measurement.download", "settings.measurement.download_detail", downloadMode,
        download
      ),
    ]
    let rows = entries.map { title, detail, mode, field in
      field.widthAnchor.constraint(equalToConstant: PaxTheme.Settings.numberWidth).isActive = true
      interfaceCopy.bind(field, key: title + "_parallel") { $0.setAccessibilityLabel($1) }
      let controls = NSStackView(views: [mode, field])
      controls.spacing = PaxTheme.Spacing.tight
      controls.widthAnchor.constraint(
        equalToConstant: PaxTheme.Settings.pickerWidth + PaxTheme.Settings.numberWidth
          + PaxTheme.Spacing.tight
      ).isActive = true
      return interfaceCopy.preference(title: title, detail: detail, control: controls)
    }
    latency.setAccessibilityIdentifier("measurement-latency-parallel")
    exit.setAccessibilityIdentifier("measurement-exit-parallel")
    download.setAccessibilityIdentifier("measurement-download-parallel")
    interfaceCopy.bind(latencyMode, key: "settings.measurement.latency_mode") {
      $0.setAccessibilityLabel($1)
    }
    interfaceCopy.bind(exitMode, key: "settings.measurement.exit_mode") {
      $0.setAccessibilityLabel($1)
    }
    interfaceCopy.bind(downloadMode, key: "settings.measurement.download_mode") {
      $0.setAccessibilityLabel($1)
    }
    interfaceCopy.button(saveButton, key: "settings.measurement.save")
    interfaceCopy.button(resetButton, key: "settings.measurement.reset")
    interfaceCopy.button(retryButton, key: "settings.retry")
    interfaceCopy.onChange = { [weak self] in self?.renderFeedback() }
    saveButton.setAccessibilityIdentifier("measurement-save")
    resetButton.setAccessibilityIdentifier("measurement-reset")
    retryButton.setAccessibilityIdentifier("measurement-retry")
    retryButton.isHidden = true
    let actions = NSStackView(views: [retryButton, NSView(), resetButton, saveButton])
    actions.spacing = PaxTheme.Spacing.tight
    let group = PaxPreferenceGroup(
      title: "", symbol: "speedometer", rows: rows, style: .compact, footer: actions)
    self.group = group
    view = interfaceCopy.group(group, key: "settings.measurement.title")
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
  private func showFeedback(_ key: String, _ parameters: [String: String] = [:]) {
    feedback = (key, parameters)
    renderFeedback()
  }
  private func renderFeedback() {
    let note = interfaceCopy.text("settings.measurement.note")
    let current = feedback.map { interfaceCopy.text($0.0, $0.1) }
    group?.apply(summary: [current, note].compactMap { $0 }.joined(separator: " · "))
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
    showFeedback("settings.measurement.loading")
    task = Task { [weak self, routes] in
      do {
        if case .preferences(let value) = try await routes.measurement(.preferences) {
          self?.baseline = value
          self?.render(value)
          self?.feedback = nil
          self?.renderFeedback()
        }
      } catch {
        self?.showFeedback(
          "settings.read_failed",
          [
            "detail": ProductErrorPresentation.display(
              error, context: self?.interfaceCopy.context ?? .chinese)
          ])
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
      showFeedback("settings.measurement.range_error")
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
    showFeedback("settings.saving")
    task = Task { [weak self, routes] in
      do {
        if case .preferences(let saved) = try await routes.measurement(.savePreferences(value)) {
          self?.baseline = saved
          self?.render(saved)
          self?.showFeedback("settings.measurement.saved")
        }
      } catch {
        self?.showFeedback(
          "settings.save_failed",
          [
            "detail": ProductErrorPresentation.display(
              error, context: self?.interfaceCopy.context ?? .chinese)
          ])
      }
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
