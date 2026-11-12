import AppKit
import PaxUI

@MainActor
final class SettingsConfirmationController: WorkbenchModalController {
  private let store: SettingsWorkbenchStore
  private let confirmation: SettingsWorkbenchStore.Confirmation
  override var maximumPresentationWidth: CGFloat? { PaxTheme.Settings.confirmationSize.width }
  init(store: SettingsWorkbenchStore, confirmation: SettingsWorkbenchStore.Confirmation) {
    self.store = store
    self.confirmation = confirmation
    let title: String
    switch confirmation {
    case .save: title = store.changes.contains { $0.field == .mode } ? "保存并重新连接？" : "关闭中断保护？"
    case .clear: title = "清除历史日志？"
    case .remove: title = "移除后台服务？"
    case .approval: title = "允许后台服务"
    }
    super.init(title: title, size: PaxTheme.Settings.confirmationSize, symbol: "gearshape")
  }
  override func loadView() {
    super.loadView()
    pane.setAccessibilityIdentifier("settings-confirmation")
    let description: String
    let confirmTitle: String
    switch confirmation {
    case .save:
      let mode = store.changes.contains { $0.field == .mode }
      description =
        mode
        ? "接管方式将在重新连接后生效，现有连接可能短暂中断。"
        : "代理意外中断时，应走代理的流量可能直接联网。主动关闭代理仍会恢复系统网络。"
      confirmTitle = mode ? "保存并重新连接" : "保存更改"
    case .clear:
      description = "删除本机已保存的连接事件和诊断记录。此操作无法撤销；线路、规则及当前连接保持不变。"
      confirmTitle = "清除日志"
    case .remove:
      description = "将先关闭代理并恢复系统网络，确认恢复完成后再移除服务。线路和规则会保留。"
      confirmTitle = "关闭代理并移除"
    case .approval:
      description = "在「系统设置 → 通用 → 登录项与扩展」中找到 Pax Connect，允许其在后台运行。完成授权后，服务状态会自动更新。"
      confirmTitle = "知道了"
    }
    pane.add(
      PaxText.label(
        description, font: PaxTheme.Typography.label(size: PaxTheme.Routes.bodyFont),
        secondary: true, wrapping: true))
    if confirmation == .save {
      for change in store.changes {
        let row = PaxPreferenceRow(
          title: change.field.title, detail: "当前：" + change.field.display(change.before),
          control: PaxText.label(
            change.field.display(change.after), font: PaxTheme.Typography.caption()))
        pane.add(row)
      }
    }
    let confirm = button(
      confirmTitle, #selector(confirm), primary: confirmation == .save || confirmation == .approval)
    if confirmation == .clear || confirmation == .remove {
      confirm.contentTintColor = PaxTheme.Color.error
    }
    confirm.setAccessibilityIdentifier("settings-confirm")
    let cancel = button("取消", #selector(close))
    cancel.keyEquivalent = "\u{1b}"
    pane.setActions(confirmation == .approval ? [confirm] : [cancel, confirm])
  }
  override func close() { store.perform(.cancel) }
  @objc private func confirm() { store.perform(.confirm) }
}
