import AppKit
import Observation
import PaxCore
import PaxUI

/// Only the confirmed log preferences are connected; other settings keep their own capability gates.
@MainActor
final class LogHistorySettingsController: NSViewController {
  private let telemetry: TelemetryStore
  private let retention = NSPopUpButton(frame: .zero, pullsDown: false)
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
    retention.addItems(withTitles: ["1小时", "12小时", "24小时"])
    retention.target = self
    retention.action = #selector(changeRetention)
    retention.selectItem(at: 2)
    retention.setAccessibilityIdentifier("history-retention")
    let group = PaxPreferenceGroup(
      title: "本地记录", symbol: "internaldrive",
      rows: [
        PaxPreferenceRow(
          title: "日志保留", detail: "最多24小时。普通列表查看30分钟、6小时或24小时；容量不足时显示实际覆盖。", control: retention),
        PaxPreferenceRow(
          title: "流量历史", detail: "流量近7天/30天只保留汇总与图表。连接明细随日志保留期清理。",
          control: PaxText.label(
            "最多30天", font: PaxTheme.Typography.label(size: PaxTheme.Routes.bodyFont))),
        PaxPreferenceRow(title: "历史日志", detail: "清除事件与原文；流量统计、线路和当前连接保持。", control: clear), status,
      ])
    view = group
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
    status.stringValue = telemetry.message.isEmpty ? telemetry.qualityMessage : telemetry.message
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
        title: "缩短日志保留时间？", detail: "超过\(hours)小时的日志与原文将被删除，无法撤销。流量汇总保持。", action: "缩短保留时间",
        run: save)
      render()
    } else {
      save()
    }
  }
  @objc private func clearHistory() {
    confirm(title: "清除本机历史日志？", detail: "事件与原文将被删除，无法撤销。新事件继续记录。", action: "清除日志") { [weak self] in
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
    alert.addButton(withTitle: "取消")
    alert.addButton(withTitle: action)
    alert.buttons.last?.keyEquivalent = ""
    alert.beginSheetModal(for: window) { response in
      if response == .alertSecondButtonReturn { run() }
    }
  }
}
