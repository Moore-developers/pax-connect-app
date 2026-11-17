import AppKit
import Observation
import PaxCore
import PaxUI

/// Each client registration is independent of entry readiness and backend availability.
@MainActor
final class MCPAccessSettingsController: NSViewController {
  private let actions: MCPAccessActions
  private let connection: ConnectionStore
  private let content = NSStackView()
  private let feedback = PaxText.label(
    "", font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  private let backend = PaxText.label(
    "", font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  private let permission = PaxRoutePicker(style: .toolbar)
  private var report: MCPAccessReport?
  private var busy = false

  init(actions: MCPAccessActions, connection: ConnectionStore) {
    self.actions = actions
    self.connection = connection
    super.init(nibName: nil, bundle: nil)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  override func loadView() {
    view = content
    content.orientation = .vertical
    content.alignment = .leading
    content.spacing = PaxTheme.Spacing.tight
    permission.addItems(withTitles: ["仅查询", "查询与诊断"])
    permission.setAccessibilityIdentifier("mcp-initial-permissions")
    permission.setAccessibilityLabel("新接入客户端权限")
    permission.widthAnchor.constraint(equalToConstant: PaxTheme.Settings.pickerWidth).isActive =
      true
    observeBackend()
    render()
  }

  override func viewWillAppear() {
    super.viewWillAppear()
    run("inspect")
  }

  private func observeBackend() {
    withObservationTracking {
      backend.stringValue =
        connection.connection == .connected
        ? "Pax 后台就绪；客户端连接状态未检测。"
        : "Pax 后台未就绪；客户端暂时不能调用业务。"
    } onChange: { [weak self] in
      Task { @MainActor in self?.observeBackend() }
    }
  }

  private func render() {
    for item in content.arrangedSubviews {
      content.removeArrangedSubview(item)
      item.removeFromSuperview()
    }
    let check = PaxRouteButton("检查状态", target: self, action: #selector(inspect))
    check.setAccessibilityIdentifier("mcp-inspect")
    check.isEnabled = !busy
    let verify = PaxRouteButton(
      report?.entry == "repair" ? "修复接入" : "验证入口", target: self, action: #selector(verifyEntry))
    verify.setAccessibilityIdentifier("mcp-verify-entry")
    verify.isEnabled = !busy && report?.entry != "missing" && report != nil
    let controls = NSStackView(views: [check, verify])
    controls.spacing = PaxTheme.Spacing.tight
    controls.widthAnchor.constraint(
      equalToConstant: check.fittingSize.width + verify.fittingSize.width + PaxTheme.Spacing.tight
    ).isActive = true
    var rows: [NSView] = [
      PaxPreferenceRow(
        title: "Pax MCP", detail: entryDescription, control: controls, style: .compact)
    ]
    for client in report?.clients ?? [] {
      let button = PaxRouteButton(
        buttonTitle(client), target: self, action: #selector(connectClient(_:)))
      button.identifier = NSUserInterfaceItemIdentifier(client.id)
      button.setAccessibilityIdentifier("mcp-connect-" + client.id)
      button.isEnabled = !busy && ["missing", "outdated", "disabled"].contains(client.state)
      rows.append(
        PaxPreferenceRow(
          title: client.name, detail: clientDescription(client), control: button, style: .compact))
    }
    rows.append(
      PaxPreferenceRow(
        title: "新接入权限", detail: "已有接入保留原权限。", control: permission, style: .compact))
    permission.isEnabled = !busy
    let note = PaxText.label(
      "接入时自动准备入口。查询结果可能发送到客户端使用的 AI 服务。",
      font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
    let footer = NSStackView(views: [backend, note, feedback])
    footer.orientation = .vertical
    footer.alignment = .leading
    footer.spacing = PaxTheme.Spacing.tight
    feedback.isHidden = feedback.stringValue.isEmpty
    for item in footer.arrangedSubviews {
      item.widthAnchor.constraint(equalTo: footer.widthAnchor).isActive = true
    }
    let group = PaxPreferenceGroup(
      title: "AI 客户端接入", symbol: "terminal", rows: rows, style: .compact, footer: footer)
    content.addArrangedSubview(group)
    group.widthAnchor.constraint(equalTo: content.widthAnchor).isActive = true
  }

  private var entryDescription: String {
    guard let report else { return "正在检查入口与客户端配置…" }
    if report.verified { return "入口验证通过；初始化与工具发现成功。" }
    switch report.entry {
    case "ready": return "入口有效，等待客户端连接；无需常驻 MCP 进程。"
    case "missing": return "尚未准备；接入客户端时自动完成。"
    default: return "入口需修复；客户端配置保持。"
    }
  }

  private func buttonTitle(_ client: MCPAccessReport.Client) -> String {
    switch client.state {
    case "configured": return "已接入"
    case "outdated": return "更新配置"
    case "disabled": return "启用接入"
    case "missing": return "接入"
    default: return "不可接入"
    }
  }

  private func clientDescription(_ client: MCPAccessReport.Client) -> String {
    let scope = client.id == "codex" ? "桌面与 CLI 共用。" : ""
    switch client.state {
    case "configured":
      return scope + "配置有效（\(client.entryName)）；权限：\(client.permissions)。"
    case "missing": return scope + "未配置 Pax；点击接入后重新加载客户端。"
    case "outdated": return scope + "Pax 入口路径需更新；保留原权限。"
    case "disabled": return scope + "Pax 配置已禁用；启用时保留原权限。"
    case "inherited": return "全局 patch 含 Pax 登记，请在客户端核对，避免重复配置。"
    case "not_found": return "未发现客户端或配置目录，请先安装并打开客户端。"
    case "unreadable": return "无法安全读取配置，请检查文件权限或路径。"
    case "invalid": return "配置格式无法识别，请先在客户端修复。"
    default: return "同名、重复或自定义配置冲突；未覆盖，请手动核对。"
    }
  }

  @objc private func inspect() { run("inspect") }
  @objc private func verifyEntry() { run(report?.entry == "repair" ? "repair" : "verify") }
  @objc private func connectClient(_ sender: NSButton) {
    guard let id = sender.identifier?.rawValue,
      let client = report?.clients.first(where: { $0.id == id })
    else { return }
    run("connect", client: client)
  }

  private func run(_ action: String, client: MCPAccessReport.Client? = nil) {
    guard !busy else { return }
    busy = true
    let allow = permission.indexOfSelectedItem == 1 ? "read,diagnose" : "read"
    feedback.stringValue = action == "inspect" ? "" : "正在处理…"
    render()
    Task { [weak self] in
      guard let self else { return }
      defer {
        busy = false
        render()
      }
      do {
        report = try await actions.run(action, client?.id ?? "", allow, client?.state ?? "")
        guard let report else { return }
        if !report.error.isEmpty {
          feedback.stringValue = message(report.error)
        } else if action == "connect" {
          feedback.stringValue = "配置已核对。请在目标客户端重新加载 MCP 或新建会话。"
        } else if action == "verify" {
          feedback.stringValue = "入口探针已退出；这不代表目标客户端已连接。"
        } else if action == "repair" {
          feedback.stringValue = "入口已修复，客户端配置未改动。"
        }
      } catch {
        feedback.stringValue = "检查失败，请确认正在运行完整的 Pax Connect 后重试。"
      }
    }
  }

  private func message(_ code: String) -> String {
    switch code {
    case "configuration_changed": return "配置已被外部修改，请检查状态后重试。"
    case "configuration_conflict": return "配置有冲突，未覆盖。请在客户端核对。"
    case "configuration_backup_failed": return "备份失败，未写入客户端配置。"
    case "configuration_commit_uncertain": return "配置可能已写入，请检查状态；原配置备份已保留。"
    case "entry_probe_failed": return "入口初始化或工具发现失败，请修复接入后重试。"
    case "installation_invalid": return "入口校验失败，请重新打开完整 App 后修复接入。"
    default: return "接入未完成，请检查目标配置；不要反复覆盖原文件。"
    }
  }
}
