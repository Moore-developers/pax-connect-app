import AppKit
import PaxCore
import PaxUI

/// Price presentation choices survive page changes; AISessionStore owns the shared session.
@MainActor final class AISessionOptions {
  var cacheChannel = true
  var channel = false
}

@MainActor final class AISessionAnalysisView: NSView {
  let store: AIUsageStore
  var data: AISessionStore? { store.data?.sessions }
  var options: AISessionOptions { store.sessionOptions }
  let contextSession = PaxCompactButton("全部会话", target: nil, action: nil)
  private var sessionPopover: NSPopover?
  let basis = PaxTabs(["官方费用", "渠道费用"])
  let cacheBasis = PaxTabs(["官方费用", "渠道费用"])
  let status = PaxInlineStatus()
  let cacheChart = PaxAnalysisChart(frame: .zero)
  private lazy var cacheCard = AIUsageLayout.card(
    AIUsageLayout.stack([
      AIUsageLayout.stack(
        [AIUsageLayout.heading("缓存读取趋势"), NSView(), cacheBasis], vertical: false),
      help("仅会话自身的全部保留请求，不含独立子会话。蓝色曲线为缓存读取 Token；堆叠面积为逐请求的缓存读取与普通输入费用，不含缓存写入和输出。"),
      cacheChart,
    ]))
  let contextTable: PaxDataTable
  let contextAgentsFooter = PaxText.label(
    "", font: PaxTheme.Typography.caption(), secondary: true)
  var contextRows: [String: AISessionContextUsage] = [:]
  var contextUnitCostRanks: [[String: Int]] = []
  var renderedContextChannel: Bool?
  var renderedContextMessage = ""
  var renderedRequests: [AISessionRow]?
  var renderedTurnRows: [AISessionRow] = []
  var renderedCacheChannel: Bool?
  var refreshing = false
  var allRows: [AISessionRow] { data?.current?.rows ?? [] }

  init(store: AIUsageStore) {
    self.store = store
    contextTable = PaxDataTable(
      columns: [
        .init("模型 + 推理强度", width: 120, growth: 2, sortable: false),
        .init(
          "上下文占比", width: 160, growth: 3, inset: PaxTheme.Spacing.xs, sortable: false,
          toolTip: "按每次请求的总输入大小分档，再按各档累计输入量计算占比；包含缓存，不含输出"),
        .init("0–150k", width: 90, numeric: true, sortable: false),
        .init("150–250k", width: 90, numeric: true, sortable: false),
        .init(">250k", width: 90, numeric: true, sortable: false),
        .init(
          "小计", width: 90, numeric: true, sortable: false,
          toolTip: "本行三档的输入费用与输入用量之和，不含输出"),
        .init("请求次数", width: 70, numeric: true, sortable: false),
        .init(
          "轮次", width: 52, numeric: true, sortable: false,
          toolTip: "按原生轮次去重，包含进行中的轮次；混合模型轮次不能相加。缺失记录显示已识别数"),
      ], metrics: .init(rowHeight: PaxTheme.AISession.contextRowHeight), selection: .none)
    super.init(frame: .zero)
    setAccessibilityIdentifier("ai-session-analysis")
    contextTable.setTableIdentifier("ai-context-usage")
    contextTable.cellProvider = { [weak self] id, column in
      self?.contextCell(id: id, column: column)
    }
    contextSession.setAccessibilityIdentifier("ai-session-context-session")
    contextSession.target = self
    contextSession.action = #selector(sessionChanged(_:))
    contextSession.capsule = true
    contextSession.capsuleHeight = PaxTheme.AISession.selectorHeight
    contextSession.heightAnchor.constraint(equalToConstant: PaxTheme.AISession.selectorHeight)
      .isActive = true
    contextSession.leadingAligned = true
    contextSession.trailingImage = PaxIcon.image(
      "chevron.down", size: PaxTheme.AISession.selectorChevronSize, weight: .semibold)
    contextSession.widthAnchor.constraint(equalToConstant: PaxTheme.AISession.selectorWidth)
      .isActive = true
    basis.onSelect = { [weak self] index in
      self?.options.channel = index == 1
      self?.refreshContext()
    }
    cacheBasis.onSelect = { [weak self] index in
      self?.options.cacheChannel = index == 1
      self?.refreshRequests()
    }
    status.onAction = { [weak self] in self?.data?.refresh() }
    cacheChart.heightAnchor.constraint(equalToConstant: PaxTheme.Chart.analysisLinkedHeight)
      .isActive = true
    cacheChart.smooth = true
    cacheChart.singleLineLegend = true
    cacheChart.kind = .lineStackedAreas
    cacheChart.strokeWidth = PaxTheme.Chart.analysisRequestStroke
    cacheChart.lineDash = PaxTheme.Chart.analysisRequestDash
    cacheChart.emptyMessage = "没有已记录的请求数据"
    let body = AIUsageLayout.stack([
      status,
      AIUsageLayout.stack([contextSession, NSView()], vertical: false),
      AIUsageLayout.card(
        AIUsageLayout.stack([
          AIUsageLayout.stack([AIUsageLayout.heading("上下文用量"), NSView(), basis], vertical: false),
          contextTable,
          contextFooter(),
        ]), verticalInset: PaxTheme.Spacing.card / 2),
      cacheCard,
    ])
    body.spacing = PaxTheme.Spacing.regular
    body.translatesAutoresizingMaskIntoConstraints = false
    addSubview(body)
    NSLayoutConstraint.activate([
      body.leadingAnchor.constraint(equalTo: leadingAnchor),
      body.trailingAnchor.constraint(equalTo: trailingAnchor),
      body.topAnchor.constraint(equalTo: topAnchor),
      body.bottomAnchor.constraint(equalTo: bottomAnchor),
    ])
    refresh()
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  func refresh() {
    guard !refreshing else { return }
    refreshing = true
    defer { refreshing = false }
    (sessionPopover?.contentViewController as? AISessionPickerController)?.refresh()
    let selectedTitle = data?.selected.first.map { title($0) } ?? "全部会话"
    contextSession.title =
      selectedTitle.count > 18 ? String(selectedTitle.prefix(18)) + "…" : selectedTitle
    contextSession.setAccessibilityLabel(selectedTitle)
    contextSession.compactStyle = data?.selected.isEmpty == false ? .selected : .bordered
    contextSession.image = data?.selected.first.flatMap { AIClient(code: $0.cli) }.flatMap {
      AIClientMark.image($0)
    }
    contextSession.toolTip =
      data?.selected.first.map { title($0) + "\n会话 ID：" + $0.session }
      ?? "所选日期内的全部会话"
    let error = data?.error ?? (data == nil ? "演示入口没有真实会话数据" : nil)
    let partial = data?.current?.truncated == true || data?.detail?.truncated == true
    let unsupported = data?.contextSupported == false
    let agentsIncomplete =
      data?.current?.contextUsage.contains {
        $0.model.isEmpty && $0.session == data?.selected.first && $0.subagentsIncomplete
      } == true
    status.isHidden =
      error == nil && !partial && !unsupported && data?.loading != true && !agentsIncomplete
    status.apply(
      error == nil ? .idle : .failure,
      text: error
        ?? (unsupported
          ? "后台不支持上下文用量，请更新后台"
          : partial
            ? "结果超过容量限制，请缩小日期范围；当前结果不代表完整统计"
            : agentsIncomplete ? "部分子代理记录缺失、未关联或仍在索引，代理统计可能不完整" : "正在读取会话…"),
      action: error == nil ? nil : "重试")
    refreshContext()
    cacheCard.isHidden = data?.selected.isEmpty != false
    refreshRequests()
  }

  @objc private func sessionChanged(_ sender: NSButton) {
    if sessionPopover?.isShown == true {
      sessionPopover?.performClose(nil)
      return
    }
    guard let data, data.catalogSupported else { return }
    let popover: NSPopover
    let picker: AISessionPickerController
    if let existing = sessionPopover,
      let controller = existing.contentViewController as? AISessionPickerController
    {
      popover = existing
      picker = controller
    } else {
      picker = AISessionPickerController(catalog: data.catalog, selected: data.selected.first)
      popover = NSPopover()
      popover.behavior = .transient
      popover.animates = !PaxMotion.reducesMotion
      popover.contentViewController = picker
      picker.onChoose = { [weak self, weak popover] key in
        self?.data?.choose(key.map { [$0] } ?? [])
        if key != nil { popover?.performClose(nil) }
        self?.refresh()
      }
      sessionPopover = popover
    }
    picker.prepare(selected: data.selected.first)
    popover.show(relativeTo: sender.bounds, of: sender, preferredEdge: .maxY)
  }
  private func title(_ key: AISessionKey) -> String {
    if let entry = data?.catalog.selected, entry.key == key {
      return AISessionPickerController.title(entry)
    }
    if let entry = data?.catalog.items.first(where: { $0.key == key }) {
      return AISessionPickerController.title(entry)
    }
    let name = allRows.first { $0.key == key && !$0.sessionTitle.isEmpty }?.sessionTitle
    return (name ?? "未命名会话 · " + String(key.session.prefix(12)))
      + " · " + AIClientMark.shortName(key.cli)
  }
  private func help(_ text: String) -> NSTextField {
    PaxText.label(text, font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  }
  func effortTone(_ value: String) -> PaxFeatureTag.Tone {
    switch value.lowercased() {
    case "light", "low": .green
    case "medium": .blue
    case "high": .yellow
    case "xhigh": .amber
    case "max": .red
    default: .neutral
    }
  }
}
