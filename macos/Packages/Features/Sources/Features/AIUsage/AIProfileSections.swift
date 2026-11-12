import AppKit
import PaxCore
import PaxUI

extension AIProfileEditor {
  func profileSection(_ title: String, symbol: String, views: [NSView], detail: String = "")
    -> NSView
  {
    let content = AIUsageLayout.stack(
      [PaxRouteSectionHeading(title, symbol: symbol, detail: detail)] + views)
    content.spacing = PaxTheme.Spacing.medium
    let card = PaxCard(style: .routesSection)
    card.contentView = content
    card.heightAnchor.constraint(
      equalTo: content.heightAnchor, constant: PaxTheme.Routes.sectionTop * 2
    ).isActive = true
    return card
  }
  func modelSummary(_ p: AIProfile) -> String {
    let sync = p.modelsChecked.map { " · 最近同步 " + TrafficFormatting.dateMinute($0) } ?? ""
    switch p.modelsState {
    case "ready": return "已读取 \(p.catalog.count) 个模型" + sync
    case "partial": return "已读取 \(p.catalog.count) 个模型 · 目录未完整返回" + sync
    case "failed":
      let reason = modelFailure(p.modelsError)
      return p.catalog.isEmpty
        ? "读取失败 · " + reason : "保留 \(p.catalog.count) 个模型 · 刷新失败 · " + reason + sync
    default: return "尚未读取模型目录"
    }
  }
  func modelFailure(_ code: String) -> String {
    switch code {
    case "models_timeout": return "连接超时"
    case "models_auth_failed": return "凭据未通过认证"
    case "models_not_found": return "供应商未提供该接口"
    case "models_invalid_response": return "返回格式无法识别"
    default: return "暂时无法连接供应商"
    }
  }
  func modelRefreshRow(_ p: AIProfile) -> NSView {
    AIUsageLayout.stack(
      [
        AIUsageLayout.label(modelSummary(p)), NSView(),
        actions.button(
          store.configuration.loading ? "读取中…" : "刷新模型",
          enabled: !store.preview && !store.configuration.loading
        ) { [weak self] in
          guard let self else { return }
          Task { [weak self] in
            guard let self else { return }
            _ = await store.configuration.refreshModels(p.id)
          }
        },
      ], vertical: false)
  }
  /// The cards below the connection form; a new Profile only gets a hint.
  func profileDetailViews() -> [NSView] {
    guard let p = current else {
      return [AIUsageLayout.label("保存后自动读取模型目录。", secondary: true)]
    }
    return [applySection(p), usageSection(p), modelsSection(p)]
  }
  private func applySection(_ p: AIProfile) -> NSView {
    let rows = store.configurableClients.filter { p.kind != "native" || p.nativeCLI == $0 }.map {
      cli -> NSView in
      let b = store.bindings[cli]
      let owns = b?.profileID == p.id
      let label = owns ? AIProfilesStore.stateLabel(b) : "未应用"
      let mark = AIUsageLayout.label(label, secondary: true)
      if label == "配置已写入" { mark.textColor = AIUsageCostDisplay.channelColor }
      return AIUsageLayout.stack(
        [
          AIUsageLayout.label(cli.rawValue), NSView(), mark,
          actions.button("切换…", enabled: store.configuration.available) { [weak self] in
            guard let self else { return }
            self.presentWorkbenchModal(
              AIBindingController(store: store, client: cli, targetProfile: p.id))
          },
        ], vertical: false)
    }
    return profileSection("应用状态", symbol: "terminal", views: rows)
  }
  private func usageSection(_ p: AIProfile) -> NSView {
    let summary = store.usageSummary(profile: p.id, lifetime: true)
    let body: NSView =
      summary.count > 0
      ? AIUsageLayout.equalRow([
        metric("Token", AIUsageFormatting.tokens(summary.tokens)),
        metric("官方费用", summary.officialCostLabel, color: AIUsageCostDisplay.officialColor),
        metric("渠道费用", summary.costLabel, color: AIUsageCostDisplay.channelColor),
        metric("请求数", String(summary.count)),
      ])
      : AIUsageLayout.label("暂无已归属请求", secondary: true)
    return profileSection(
      "累计用量", symbol: "chart.bar", views: [body], detail: "全部历史 · 本地估算，非平台账单")
  }
  private func metric(_ title: String, _ value: String, color: NSColor? = nil) -> NSView {
    let number = AIUsageLayout.label(value, metric: true)
    if let color { number.textColor = color }
    return AIUsageLayout.stack([AIUsageLayout.label(title, secondary: true), number])
  }
  private func modelsSection(_ p: AIProfile) -> NSView {
    if p.kind == "native" {
      return profileSection(
        "模型", symbol: "sparkles",
        views: [AIUsageLayout.label("原生账号配置不查询 API 模型目录", secondary: true)])
    }
    var views: [NSView] = [modelRefreshRow(p)]
    let split = usedModels(p)
    if !split.used.isEmpty { views.append(modelTable(p, split.used)) }
    if !split.unused.isEmpty {
      let table = modelTable(p, split.unused)
      table.isHidden = !showUnusedModels
      let title = { [self] in unusedTitle(split.unused.count, hasUsed: !split.used.isEmpty) }
      let box = ToggleBox()
      let toggle = actions.button(title()) { [weak self, weak table] in
        guard let self, let table else { return }
        showUnusedModels.toggle()
        pane.performContentUpdates { table.isHidden = !showUnusedModels }
        box.button?.title = title()
      }
      box.button = toggle
      toggle.compactStyle = .text
      views += [AIUsageLayout.wrap(toggle), table]
    }
    return profileSection("模型", symbol: "sparkles", views: views)
  }
  private func unusedTitle(_ n: Int, hasUsed: Bool) -> String {
    (showUnusedModels ? "收起 " : "") + (hasUsed ? "其余 \(n) 个未使用" : "\(n) 个模型暂无用量")
      + (showUnusedModels ? "" : " ›")
  }
  /// Catalog models only; used ones first, by cost.
  private func usedModels(_ p: AIProfile) -> (used: [AIProviderModel], unused: [AIProviderModel]) {
    let rated = p.catalog.map {
      ($0, store.usageSummary(model: $0.id, profile: p.id, lifetime: true))
    }
    let used = rated.filter { $0.1.count > 0 }.sorted {
      $0.1.cost != $1.1.cost ? $0.1.cost > $1.1.cost : $0.0.id < $1.0.id
    }
    return (used.map(\.0), rated.filter { $0.1.count == 0 }.map(\.0))
  }
  private func modelTable(_ p: AIProfile, _ models: [AIProviderModel]) -> PaxDataTable {
    let owners = Set(p.catalog.map(\.owner))
    let showOwner = owners.count > 1 && owners.contains { !$0.isEmpty }
    let showContext = p.catalog.contains { $0.contextWindow > 0 }
    var columns: [PaxDataTable.Column] = [
      .init("模型 ID", width: 190, growth: 1.6, symbol: "sparkles", sortable: false)
    ]
    if showOwner { columns.append(.init("提供方", width: 110, growth: 0.3, sortable: false)) }
    if showContext { columns.append(.init("上下文", width: 100, growth: 0, sortable: false)) }
    columns.append(.init("Token", width: 96, growth: 0, sortable: false))
    let costColumn = columns.count
    columns.append(
      .init(
        "官方 / 渠道费用", width: 190, growth: 0, inset: PaxTheme.Spacing.regular * 1.5,
        sortable: false))
    let table = PaxDataTable(
      columns: columns,
      rows: models.map { m in
        let usage = store.usageSummary(model: m.id, profile: p.id, lifetime: true)
        var values = [m.id]
        if showOwner { values.append(m.owner.isEmpty ? "—" : m.owner) }
        if showContext {
          values.append(m.contextWindow > 0 ? AIUsageFormatting.tokens(Int(m.contextWindow)) : "—")
        }
        values.append(usage.count > 0 ? AIUsageFormatting.tokens(usage.tokens) : "—")
        values.append(
          usage.count > 0
            ? AIUsageCostDisplay.text(official: usage.officialCostLabel, channel: usage.costLabel)
            : "—")
        return .init(id: m.id, values: values)
      }, metrics: .workbench(rowHeight: PaxTheme.Dashboard.rowHeight, rounded: true),
      selection: .none)
    table.cellProvider = { [weak self] id, column in
      guard column == costColumn, let self else { return nil }
      let usage = self.store.usageSummary(model: id, profile: p.id, lifetime: true)
      return AIUsageCostDisplay.label(
        official: usage.count > 0 ? usage.officialCostLabel : "—",
        channel: usage.count > 0 ? usage.costLabel : "—", stacked: true)
    }
    table.setTableIdentifier("ai-profile-model-catalog")
    return table
  }
}

@MainActor private final class ToggleBox { weak var button: PaxCompactButton? }
