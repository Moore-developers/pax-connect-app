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
  private let status = PaxText.label("", font: PaxTheme.Typography.caption(), wrapping: true)
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
    let note = interfaceCopy.label(
      "settings.logs.note",
      font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
    let footer = NSStackView(views: [note, status])
    footer.orientation = .vertical
    footer.alignment = .leading
    footer.spacing = PaxTheme.Spacing.tight
    for item in footer.arrangedSubviews {
      item.widthAnchor.constraint(equalTo: footer.widthAnchor).isActive = true
    }
    let group = PaxPreferenceGroup(
      title: "", symbol: "internaldrive",
      rows: [
        interfaceCopy.preference(
          title: "settings.logs.retention", detail: "settings.logs.retention_detail",
          control: retention),
        interfaceCopy.preference(
          title: "settings.logs.history", detail: "settings.logs.history_detail", control: clear),
      ], style: .compact, footer: footer)
    view = interfaceCopy.group(group, key: "settings.logs.title")
    interfaceCopy.onChange = { [weak self] in self?.render() }
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
    let message = telemetry.message(context: context)
    status.stringValue = message.isEmpty ? telemetry.qualityMessage(context: context) : message
    status.isHidden = status.stringValue.isEmpty
  }
  @objc private func changeRetention() {
    guard retentionHours.indices.contains(retention.indexOfSelectedItem) else { return }
    let hours = retentionHours[retention.indexOfSelectedItem]
    let save: @MainActor () -> Void = { [weak self] in
      guard let self else { return }
      Task {
        _ = await self.telemetry.saveRetention(hours)
        self.render()
      }
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
      guard let self else { return }
      Task { _ = await self.telemetry.clearLogs() }
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
