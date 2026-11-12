import AppKit
import Observation
import PaxCore
import PaxUI

@MainActor
final class AIBindingController: WorkbenchModalController {
  static let popoverWidth: CGFloat = 420
  private let store: AIUsageStore
  private let client: AIClient
  private let targetProfile: String?
  private let actions = AIUsageActions()
  var onDismiss: (() -> Void)?
  private var applying = false
  private var applyButtons: [NSButton] = []
  private let model = PaxRouteField("")
  private let explanation = PaxText.label(
    "", font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  init(store: AIUsageStore, client: AIClient, targetProfile: String? = nil) {
    self.store = store
    self.client = client
    self.targetProfile = targetProfile
    super.init(title: client.rawValue + " 配置", style: .compact, symbol: client.symbol)
  }
  override var maximumPresentationWidth: CGFloat? { Self.popoverWidth }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    super.loadView()
    render()
    observeAvailability()
  }
  private func observeAvailability() {
    withObservationTracking {
      let enabled = store.preview || (store.configuration.available && !store.configuration.loading)
      for button in applyButtons { button.isEnabled = enabled }
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observeAvailability() }
    }
  }
  override func viewDidAppear() {
    super.viewDidAppear()
    guard !store.preview else { return }
    Task { [weak self] in
      guard let self else { return }
      await store.configuration.refresh()
      if !applying { render() }
    }
  }
  private func note(_ text: String) -> NSTextField {
    PaxText.label(text, font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  }
  private func render() {
    pane.clear()
    actions.reset()
    applyButtons.removeAll()
    let binding = store.bindings[client]
    var configurationRows: [NSView] = []
    let status = PaxBadge(
      title: AIProfilesStore.stateLabel(binding),
      tone: AIUsageStore.tone(forState: AIProfilesStore.stateLabel(binding)))
    let recheck = actions.button("重新检查") { [weak self] in self?.recheck() }
    recheck.capsule = false
    let stateRow = AIUsageLayout.stack(
      [status, NSView()] + (store.preview ? [] : [recheck]), vertical: false)
    configurationRows.append(stateRow)
    if let binding, !binding.targetPath.isEmpty {
      configurationRows.append(note(binding.targetPath))
    }
    if !store.preview {
      switch binding?.state {
      case "ready", "changed":
        let button = actions.button("导入") { [weak self] in self?.importForm() }
        button.capsule = false
        configurationRows.append(
          PaxPreferenceRow(
            title: "导入当前配置", detail: "仅保存到 Pax，不修改客户端。", control: button))
      case "missing":
        configurationRows.append(note("尚未配置 API。可选择已有 Profile，或先创建再应用。"))
      case "account":
        configurationRows.append(note("检测到官方账号配置。登录与订阅由客户端管理，本地用量无需 Profile 即可读取。"))
      case "invalid", "recovery_required":
        configurationRows.append(note("配置格式、权限或认证需要检查。未覆盖文件，处理后可重新检查。"))
      default: break
      }
    }
    pane.add(
      PaxPreferenceGroup(
        title: "客户端配置", symbol: "terminal", rows: [AIUsageLayout.stack(configurationRows)]))

    let accountHelp = actions.button("登录说明") { [weak self] in self?.showAccountHelp() }
    accountHelp.capsule = false
    pane.add(
      PaxPreferenceGroup(
        title: "官方账号", symbol: "person.crop.circle",
        rows: [
          PaxPreferenceRow(
            title: "在客户端管理登录",
            detail: "订阅账号与 API 独立计费。Pax 读取本机日志，不需要账号 Profile。",
            control: accountHelp)
        ]))

    let profiles = store.profiles.filter {
      (targetProfile == nil || targetProfile == $0.id)
        && ($0.kind != "native" || $0.nativeCLI == client)
    }
    let profileRows: [NSView] = profiles.map { p in
      let current = binding?.profileID == p.id
      let applied = current && binding?.state == "applied"
      let control: NSView
      if applied {
        control = PaxBadge(title: "配置已写入", tone: .healthy)
      } else {
        let button = actions.button(
          "应用…",
          enabled: store.preview
            || (store.configuration.available && !store.configuration.loading
              && ["missing", "ready", "applied", "pending", "changed"].contains(
                binding?.state ?? ""))
        ) { [weak self] in self?.prepare(p) }
        button.capsule = false
        if ["missing", "ready", "applied", "pending", "changed"].contains(binding?.state ?? "") {
          applyButtons.append(button)
        }
        button.setAccessibilityLabel("应用 " + p.name)
        control = button
      }
      let source =
        p.kind == "native" ? "原生账号" : p.serviceSource.label + " · " + AIUsageStore.host(p.endpoint)
      let detail =
        source + " · " + AIUsageFormatting.multiplier(p.multiplier)
        + (current && !applied ? " · " + AIProfilesStore.stateLabel(binding) : "")
      return PaxPreferenceRow(title: p.name, detail: detail, control: control)
    }
    pane.add(
      PaxPreferenceGroup(
        title: "可用 Profile", symbol: "square.stack",
        rows: profileRows.isEmpty ? [note("暂无可用 Profile")] : profileRows))
    if !store.preview {
      let create = actions.button("创建…") { [weak self] in self?.createForm() }
      create.capsule = false
      pane.add(
        PaxPreferenceGroup(
          title: "新增 Profile", symbol: "plus.circle",
          rows: [
            PaxPreferenceRow(
              title: "创建 Profile", detail: "保存 API 地址与 Key，稍后选择应用。", control: create)
          ]))
    }
    explanation.stringValue = "官方 API 和中转都使用地址与 Key；倍率仅用于本地估算。保存后再应用，当前会话是否采用以客户端为准。"
    pane.add(explanation)
    pane.setActions([actions.button("关闭") { [weak self] in self?.close() }])
    pane.invalidateContentSize()
  }
  private func showAccountHelp() {
    pane.clear()
    let login =
      client == .codex
      ? "停用 API 认证，恢复默认 OpenAI 服务。\n运行 codex login 后开启新会话，再重新检查。"
      : "停用 API 地址与 Key，运行 /login 登录。\n开启新会话后，点「重新检查」。"
    pane.add(
      PaxPreferenceGroup(
        title: "使用官方账号", symbol: "person.crop.circle",
        rows: [note(login)]))
    pane.add(
      PaxPreferenceGroup(
        title: "改用 API", symbol: "key",
        rows: [note("官方 API 与中转均使用地址和 Key。\n在客户端完成 API 认证，再选择 Profile 应用。")]))
    pane.add(
      PaxPreferenceGroup(
        title: "用量与费用", symbol: "chart.bar",
        rows: [note("无需 Profile，自动读取本机 CLI 用量。\n费用为 API 等值估算，非订阅扣费；不含剩余额度。")]))
    let recheck = actions.button("重新检查", primary: true) { [weak self] in self?.recheck() }
    recheck.widthAnchor.constraint(
      greaterThanOrEqualToConstant: ceil(recheck.intrinsicContentSize.width) + PaxTheme.Spacing.xs
    ).isActive = true
    pane.setActions([
      actions.button("返回") { [weak self] in self?.render() }, recheck,
    ])
    pane.invalidateContentSize()
  }
  private func recheck() {
    guard !applying else { return }
    applying = true
    Task { [weak self] in
      guard let self else { return }
      await store.configuration.refresh()
      applying = false
      render()
      if let error = store.configuration.error { report(error, failed: true) }
    }
  }
  private func createForm() {
    pane.clear()
    let name = field("名称", value: "", identifier: "ai-create-name")
    let endpoint = field("API 地址", value: "https://", identifier: "ai-create-endpoint")
    let rate = field("计费倍率", value: "1", identifier: "ai-create-rate")
    let key = PaxRouteSecureField(placeholder: "输入 API Key")
    key.setAccessibilityIdentifier("ai-create-key")
    for view in [name.0, endpoint.0, rate.0, note("API Key"), key] { pane.add(view) }
    pane.add(note("保存仅写入 Pax。保存后再选择应用，不会立即改动客户端。费用按官方单价乘倍率估算。"))
    pane.add(explanation)
    explanation.stringValue = ""
    pane.setActions([
      actions.button("返回") { [weak self] in
        key.stringValue = ""
        self?.render()
      },
      actions.button("保存 Profile", primary: true) { [weak self] in
        guard let self, !applying else { return }
        guard let multiplier = Double(rate.1.stringValue) else {
          report("请填写有效倍率", failed: true)
          return
        }
        let profile = AIProfile(
          id: UUID().uuidString,
          name: name.1.stringValue.trimmingCharacters(in: .whitespacesAndNewlines),
          endpoint: endpoint.1.stringValue, multiplier: multiplier)
        let secret = key.stringValue
        applying = true
        Task { [weak self] in
          guard let self else { return }
          let ok = await store.configuration.save(profile, key: secret)
          applying = false
          if ok {
            key.stringValue = ""
            render()
            report("Profile 已保存。请选择此配置应用到客户端。")
          } else {
            report(store.configuration.error ?? "保存失败", failed: true)
          }
        }
      },
    ])
    pane.invalidateContentSize()
  }
  private func importForm() {
    guard let binding = store.bindings[client] else { return }
    pane.clear()
    let host = URL(string: binding.endpoint)?.host ?? client.rawValue
    let name = field("名称", value: host, identifier: "ai-import-name")
    let rate = field("计费倍率", value: "1", identifier: "ai-import-rate")
    pane.add(note("从 " + client.rawValue + " 导入 · " + binding.endpoint))
    pane.add(note("Key 已检测，仅由后台保存，不显示明文。导入不会改写客户端配置或已有历史费用。"))
    pane.add(name.0)
    pane.add(rate.0)
    pane.add(explanation)
    explanation.stringValue = ""
    pane.setActions([
      actions.button("返回") { [weak self] in self?.render() },
      actions.button("确认导入", primary: true) { [weak self] in
        guard let self, !applying else { return }
        guard let multiplier = Double(rate.1.stringValue) else {
          report("请填写有效倍率", failed: true)
          return
        }
        applying = true
        Task { [weak self] in
          guard let self else { return }
          let ok = await store.configuration.importCurrent(
            client, name: name.1.stringValue, multiplier: multiplier)
          applying = false
          if ok {
            render()
            report("已导入到 Pax，客户端配置保持原样。")
          } else {
            report(store.configuration.error ?? "导入失败，请重新检查配置", failed: true)
          }
        }
      },
    ])
    pane.invalidateContentSize()
  }
  private func prepare(_ profile: AIProfile) {
    guard !applying else { return }
    if store.preview {
      store.configuration.previewBind(client, profile: profile)
      close()
      return
    }
    if store.bindings[client]?.state == "missing" {
      pane.clear()
      pane.add(note("将把「" + profile.name + "」的 API 地址与 Key 写入 " + client.rawValue + "。"))
      pane.add(note(store.bindings[client]?.targetPath ?? ""))
      if store.bindings[client]?.requiresModel == true {
        pane.add(note("模型 ID（首次配置必填）"))
        model.placeholderString = "填写服务商提供的准确模型 ID"
        model.setAccessibilityIdentifier("ai-initialize-model")
        pane.add(model)
        if !profile.catalog.isEmpty {
          pane.add(note("此服务已读取的模型：" + profile.catalog.prefix(8).map(\.id).joined(separator: "、")))
        }
      }
      pane.add(note("只创建或补充必要 API 设置，保留已有 MCP 等配置。账号登录或认证冲突会停止写入。"))
      pane.add(explanation)
      explanation.stringValue = ""
      pane.setActions([
        actions.button("返回") { [weak self] in self?.render() },
        actions.button("创建并应用", primary: true) { [weak self] in
          self?.apply(profile, initialize: true)
        },
      ])
      pane.invalidateContentSize()
    } else {
      apply(profile, initialize: false)
    }
  }
  private func apply(_ profile: AIProfile, initialize: Bool) {
    guard !applying else { return }
    applying = true
    report("正在核对配置…")
    Task { [weak self] in
      guard let self else { return }
      defer { applying = false }
      await store.configuration.refresh()
      guard
        let plan = await store.configuration.plan(
          client, profile: profile, initialize: initialize, modelID: model.stringValue)
      else {
        report(store.configuration.error ?? "无法生成写入计划，请重新检查", failed: true)
        return
      }
      let ok = await store.configuration.apply(plan)
      if ok {
        render()
        report("配置已写入。请按客户端要求开启新会话；实际请求成功后，用量会自动显示。")
      } else {
        report(store.configuration.error ?? "配置写入失败，请重试", failed: true)
      }
    }
  }
  private func report(_ text: String, failed: Bool = false) {
    explanation.stringValue = text
    explanation.textColor = failed ? PaxTheme.Color.error : PaxTheme.Color.textSecondary
    pane.invalidateContentSize()
  }
  override func close() {
    guard !applying else { return }
    super.close()
  }
  override func viewDidDisappear() {
    super.viewDidDisappear()
    onDismiss?()
    onDismiss = nil
  }
}
