import Foundation
import Observation

/// 设置工作台专用草稿；不持久化、不注册服务，也不操作网络或真实日志。
@MainActor @Observable
final class SettingsWorkbenchStore {
  enum Category: Int, CaseIterable {
    case general, connection, data, service, measurement
    var title: String { ["通用", "连接", "更新与数据", "后台服务", "线路测量"][rawValue] }
    var symbol: String {
      ["slider.horizontal.3", "network", "arrow.clockwise", "server.rack", "speedometer"][rawValue]
    }
  }
  enum Field: String, CaseIterable {
    case login, connect, dock, mode, fastest, recovery, guardTraffic, lan
    case subscription, rules, retention
    var title: String {
      switch self {
      case .login: "登录时启动"
      case .connect: "启动后自动连接"
      case .dock: "显示在程序坞"
      case .mode: "接管方式"
      case .fastest: "自动选择最快线路"
      case .recovery: "切网后自动恢复"
      case .guardTraffic: "连接中断时阻止绕行"
      case .lan: "局域网直连"
      case .subscription: "订阅自动更新"
      case .rules: "规则自动更新"
      case .retention: "日志保留时间"
      }
    }
    var detail: String {
      switch self {
      case .login: "登录 Mac 后打开 Pax Connect。"
      case .connect: "使用上次选择的线路。"
      case .dock: "关闭后，仍可从菜单栏打开主窗口。"
      case .mode: "TUN 接管本机网络流量。"
      case .fastest: "在当前分组的可用线路中自动选择。"
      case .recovery: "切换 Wi-Fi 或从睡眠唤醒后，重新检查连接。"
      case .guardTraffic: "代理意外中断时，阻止应走代理的流量直接联网。"
      case .lan: "打印机、NAS 等本地设备直接访问。"
      case .subscription: "更新已有订阅中的线路。"
      case .rules: "更新失败时继续使用上一有效版本。"
      case .retention: "到期后自动清理历史日志。"
      }
    }
    var options: [String]? {
      switch self {
      case .mode: ["TUN", "系统代理"]
      case .subscription: ["手动", "每 6 小时", "每天"]
      case .rules: ["手动", "每天"]
      case .retention: ["最多24小时"]
      default: nil
      }
    }
    var category: Category {
      switch self {
      case .login, .connect, .dock: .general
      case .subscription, .rules, .retention: .data
      default: .connection
      }
    }
    var initial: Int { self == .retention ? 0 : self == .connect || self == .mode ? 0 : 1 }
    func display(_ value: Int) -> String {
      if let options, options.indices.contains(value) { return options[value] }
      return value == 1 ? "已开启" : "已关闭"
    }
  }
  enum Service { case running, disconnected, pending, absent, unavailable }
  enum Scene: String, CaseIterable {
    case normal = "正常"
    case disconnected = "后台断线"
    case pending = "等待批准"
    case absent = "未安装"
    case saveFailure = "下次保存失败"
    case serviceFailure = "下次服务操作失败"
  }
  enum Confirmation: Equatable { case save, clear, remove, approval }
  enum Operation { case save, clear, remove, enable, reconnect }
  enum Intent {
    case select(Category)
    case edit(Field, Int)
    case discard, save
    case clear, manageService, confirm, cancel, toggleAdvanced
    case scene(Scene)
  }
  struct Change: Equatable {
    let field: Field
    let before: Int
    let after: Int
  }
  private(set) var category: Category = .general
  private(set) var saved = Dictionary(uniqueKeysWithValues: Field.allCases.map { ($0, $0.initial) })
  private(set) var draft = Dictionary(uniqueKeysWithValues: Field.allCases.map { ($0, $0.initial) })
  private(set) var service: Service = .running
  private(set) var confirmation: Confirmation?
  private(set) var operation: Operation?
  private(set) var expanded = false
  private(set) var logsCleared = false
  private(set) var message = ""
  private(set) var failedOperation: Operation?
  private var failNextSave = false
  private var failNextService = false
  private var task: Task<Void, Never>?
  let backendAvailable: Bool
  private let delay: Duration
  private let onClearLogs: () -> Void
  private let onRemoveService: () -> Void
  init(
    delay: Duration = .milliseconds(400), onClearLogs: @escaping () -> Void = {},
    onRemoveService: @escaping () -> Void = {}, backendAvailable: Bool = true
  ) {
    self.backendAvailable = backendAvailable
    if !backendAvailable { service = .unavailable }
    self.delay = delay
    self.onClearLogs = onClearLogs
    self.onRemoveService = onRemoveService
  }
  isolated deinit { task?.cancel() }
  var changes: [Change] {
    Field.allCases.compactMap { field in
      let before = saved[field, default: field.initial]
      let after = value(field)
      return before == after ? nil : Change(field: field, before: before, after: after)
    }
  }
  var busy: Bool { operation != nil }
  var failed: Bool { failedOperation != nil }
  var canSave: Bool {
    backendAvailable && !busy && !changes.isEmpty
      && (service == .running || changes.allSatisfy { $0.field.category == .general })
  }
  var requiresConfirmation: Bool {
    changes.contains { $0.field == .mode || ($0.field == .guardTraffic && $0.after == 0) }
  }
  var status: String {
    if !backendAvailable { return "常规设置尚未接入后台，暂不可更改" }
    if !message.isEmpty { return message }
    if !changes.isEmpty && !canSave && !busy {
      return "后台服务不可用，\(changes.count) 项更改已保留"
    }
    return changes.isEmpty ? "所有更改已保存" : "\(changes.count) 项更改尚未保存"
  }
  var banner: String {
    switch service {
    case .unavailable: "常规设置尚未接入后台；线路测量可独立配置。"
    case .running: ""
    case .disconnected: "后台服务连接已断开，连接与数据设置暂不可更改。已有草稿保留。"
    case .pending: "请在系统设置中允许后台服务。通用设置仍可修改。"
    case .absent: "后台服务未安装。启用后可设置连接与自动更新。"
    }
  }
  func value(_ field: Field) -> Int { draft[field, default: field.initial] }
  func canEdit(_ field: Field) -> Bool {
    backendAvailable && !busy && confirmation == nil
      && (service == .running || field.category == .general)
      && (field != .guardTraffic || value(.mode) == 0)
  }
  func detail(_ field: Field) -> String {
    if field == .mode && value(.mode) == 1 {
      return "仅接管遵循系统代理设置的应用，不能保护全部流量。"
    }
    if field == .guardTraffic {
      if value(.mode) == 1 { return "仅 TUN 模式可用；系统代理无法阻止其他流量绕行。" }
      if value(field) == 0 { return "已关闭。代理意外中断时，应走代理的流量可能直接联网。" }
    }
    return field.detail
  }
  func perform(_ intent: Intent) {
    switch intent {
    case .select(let value): category = value
    case .edit(let field, let value):
      guard canEdit(field), (field.options ?? ["关闭", "开启"]).indices.contains(value) else { return }
      draft[field] = value
      message = ""
      failedOperation = nil
    case .discard:
      guard !busy && confirmation == nil else { return }
      draft = saved
      message = "已撤销未保存的更改"
      failedOperation = nil
    case .save:
      guard canSave && confirmation == nil else { return }
      if requiresConfirmation { confirmation = .save } else { start(.save) }
    case .clear:
      if !busy && confirmation == nil && service == .running { confirmation = .clear }
    case .manageService:
      guard !busy && confirmation == nil else { return }
      switch service {
      case .running: confirmation = .remove
      case .disconnected: start(.reconnect)
      case .pending: confirmation = .approval
      case .absent: start(.enable)
      case .unavailable: break
      }
    case .cancel: confirmation = nil
    case .confirm:
      let requested = confirmation
      confirmation = nil
      switch requested {
      case .save: if canSave { start(.save) }
      case .clear: if service == .running { start(.clear) }
      case .remove: if service == .running { start(.remove) }
      case .approval, nil: break
      }
    case .toggleAdvanced: expanded.toggle()
    case .scene(let scene):
      guard backendAvailable else { return }
      task?.cancel()
      task = nil
      operation = nil
      confirmation = nil
      message = ""
      failedOperation = nil
      failNextSave = scene == .saveFailure
      failNextService = scene == .serviceFailure
      switch scene {
      case .disconnected: service = .disconnected
      case .pending: service = .pending
      case .absent: service = .absent
      default: service = .running
      }
    }
  }
  private func start(_ operation: Operation) {
    guard !busy else { return }
    self.operation = operation
    failedOperation = nil
    switch operation {
    case .save: message = "正在保存…"
    case .clear: message = "正在清除历史日志…"
    case .remove: message = "正在停止代理并恢复网络…"
    case .enable: message = "正在登记后台服务…"
    case .reconnect: message = "正在重新连接…"
    }
    let delay = delay
    task = Task { @MainActor [weak self] in
      do { try await Task.sleep(for: delay) } catch { return }
      guard !Task.isCancelled, let self else { return }
      self.complete(operation)
    }
  }
  private func complete(_ operation: Operation) {
    self.operation = nil
    task = nil
    if operation == .save && failNextSave {
      failNextSave = false
      failedOperation = operation
      message = "保存失败，更改已保留。请重试。"
      return
    }
    if [.remove, .enable, .reconnect].contains(operation) && failNextService {
      failNextService = false
      failedOperation = operation
      message = "服务操作失败，保留原有服务状态。请重试。"
      return
    }
    switch operation {
    case .save:
      saved = draft
      message = "更改已保存"
    case .clear:
      onClearLogs()
      logsCleared = true
      message = "历史日志已清除"
    case .remove:
      onRemoveService()
      service = .absent
      message = "代理已关闭，网络已恢复，后台服务已移除"
    case .enable:
      service = .pending
      message = "服务已登记，等待系统批准"
    case .reconnect:
      service = .running
      message = "已重新连接后台服务"
    }
  }
}
