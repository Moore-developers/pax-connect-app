import AppKit
import Observation
import PaxCore
import PaxUI

/// Only the confirmed log preferences are connected; other settings keep their own capability gates.
@MainActor
final class LogHistorySettingsController: NSViewController, InterfaceLanguageConsumer {
  let interfaceCopy = InterfaceCopyBindings()
  private let telemetry: TelemetryStore
  private let retention = PaxRoutePicker(style: .toolbar)
  private let retentionHours: [UInt32] = [1, 12, 24]
  private enum Action { case retention, clear }
  private var feedbackAction: Action?
  private var retentionRow: PaxPreferenceRow?
  private var historyRow: PaxPreferenceRow?
  private var group: PaxPreferenceGroup?
  private let storageReadout = PaxHoverReadout()
  private let storageLabel = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private lazy var storageHost = PaxHoverReadoutHost(storageLabel, readout: storageReadout) {
    [weak self] in
    guard let self, telemetry.connected, let storage = telemetry.history.sqliteStorage else {
      return nil
    }
    return LogStorageReadout.view(storage, context: interfaceCopy.context)
  }
  private lazy var clear = PaxRouteButton("清除日志…", target: self, action: #selector(clearHistory))
  init(telemetry: TelemetryStore) {
    self.telemetry = telemetry
    super.init(nibName: nil, bundle: nil)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    clear.setAccessibilityIdentifier("history-clear")
    interfaceCopy.picker(
      retention,
      keys: ["settings.logs.one_hour", "settings.logs.twelve_hours", "settings.logs.day"])
    retention.widthAnchor.constraint(equalToConstant: PaxTheme.Settings.pickerWidth).isActive = true
    interfaceCopy.bind(retention, key: "settings.logs.retention_accessibility") {
      $0.setAccessibilityLabel($1)
    }
    interfaceCopy.button(clear, key: "settings.logs.clear")
    retention.target = self
    retention.action = #selector(changeRetention)
    retention.selectItem(at: 2)
    retention.setAccessibilityIdentifier("history-retention")
    let retentionRow = interfaceCopy.preference(
      title: "settings.logs.retention", detail: "settings.logs.retention_detail", control: retention
    )
    let historyRow = interfaceCopy.preference(
      title: "settings.logs.history", detail: "settings.logs.history_detail", control: clear)
    self.retentionRow = retentionRow
    self.historyRow = historyRow
    storageHost.setAccessibilityElement(true)
    storageHost.setAccessibilityRole(.button)
    storageHost.setAccessibilityIdentifier("history-storage-readout")
    storageHost.setContentCompressionResistancePriority(.required, for: .horizontal)
    storageLabel.setContentCompressionResistancePriority(.required, for: .horizontal)
    interfaceCopy.onChange = { [weak self] in
      self?.storageReadout.close()
      self?.render()
    }
    let group = PaxPreferenceGroup(
      title: "", symbol: "internaldrive", rows: [retentionRow, historyRow], style: .compact,
      headingAccessory: storageHost)
    self.group = group
    view = interfaceCopy.group(group, key: "settings.logs.title")
    observe()
  }
  private func observe() {
    withObservationTracking {
      render()
    } onChange: { [weak self] in
      Task { @MainActor in self?.observe() }
    }
  }
  private func render() {
    clear.isEnabled = telemetry.available && !telemetry.busy
    retention.isEnabled = telemetry.available && !telemetry.busy
    retention.selectItem(
      at: retentionHours.firstIndex(of: telemetry.settings.logRetentionHours) ?? 2)
    let context = interfaceCopy.context
    let storage = telemetry.connected ? telemetry.history.sqliteStorage : nil
    let size = storage.map { TrafficFormatting.bytes($0.totalBytes) } ?? "—"
    let total = context.text("settings.logs.storage_size", ["size": size])
    storageLabel.stringValue = total
    storageHost.setAccessibilityLabel(total)
    storageHost.setAccessibilityHelp(
      storage.map { LogStorageReadout.accessibilityText($0, context: context) })
    if storage == nil { storageReadout.close() }
    group?.apply(summary: telemetry.qualityMessage(context: context))
    let message = telemetry.available ? telemetry.message(context: context) : ""
    retentionRow?.apply(feedback: feedbackAction == .retention ? message : "")
    historyRow?.apply(feedback: feedbackAction == .clear ? message : "")
  }
  @objc private func changeRetention() {
    guard retentionHours.indices.contains(retention.indexOfSelectedItem) else { return }
    let hours = retentionHours[retention.indexOfSelectedItem]
    let save: @MainActor () -> Void = { [weak self] in
      self?.perform(.retention) { await $0.saveRetention(hours) }
    }
    if hours < telemetry.settings.logRetentionHours {
      confirm(
        title: interfaceCopy.text("settings.logs.shorten_title"),
        detail: interfaceCopy.text("settings.logs.shorten_detail", ["hours": String(hours)]),
        action: interfaceCopy.text("settings.logs.shorten"),
        run: save)
      render()
    } else {
      save()
    }
  }
  @objc private func clearHistory() {
    confirm(
      title: interfaceCopy.text("settings.logs.clear_title"),
      detail: interfaceCopy.text("settings.logs.clear_detail"),
      action: interfaceCopy.text("settings.logs.clear_confirm")
    ) { [weak self] in
      self?.perform(.clear) { await $0.clearLogs() }
    }
  }
  private func perform(
    _ action: Action, operation: @escaping @MainActor (TelemetryStore) async -> Bool
  ) {
    guard telemetry.available, !telemetry.busy else { return }
    feedbackAction = nil
    render()
    Task {
      _ = await operation(telemetry)
      feedbackAction = action
      render()
    }
  }
  private func confirm(
    title: String, detail: String, action: String, run: @escaping @MainActor () -> Void
  ) {
    guard let window = view.window else { return }
    let alert = NSAlert()
    alert.messageText = title
    alert.informativeText = detail
    alert.addButton(withTitle: interfaceCopy.text("common.cancel"))
    alert.addButton(withTitle: action)
    alert.buttons.last?.keyEquivalent = ""
    alert.beginSheetModal(for: window) { response in
      if response == .alertSecondButtonReturn { run() }
    }
  }
}
