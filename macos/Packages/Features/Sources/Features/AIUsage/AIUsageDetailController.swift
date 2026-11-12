import AppKit
import Observation
import PaxCore
import PaxUI

@MainActor
final class AIUsageDetailController: WorkbenchModalController {
  static let statisticsWidth: CGFloat = 420
  static let inspectorWidth: CGFloat = 650
  static let editorWidth: CGFloat = 600
  enum Kind {
    case cost, tokens, requests
    case model(String)
    case request(String)
  }
  private let store: AIUsageStore
  private let kind: Kind
  private var invalidation: UInt64 = 0
  private let actions = AIUsageActions()
  private var isRequest: Bool {
    if case .request = kind { return true }
    return false
  }
  override var alignsToTrailingEdge: Bool {
    if case .model = kind { return true }
    return false
  }
  override var maximumPresentationWidth: CGFloat? { preferredContentSize.width }
  init(store: AIUsageStore, kind: Kind) {
    self.store = store
    self.kind = kind
    let title: String
    let width: CGFloat
    switch kind {
    case .cost:
      title = "估算费用"
      width = Self.statisticsWidth
    case .tokens:
      title = "Token 用量"
      width = Self.statisticsWidth
    case .requests:
      title = "请求统计"
      width = Self.statisticsWidth
    case .model(let id):
      title = store.model(id) + " · 用量分析"
      width = Self.inspectorWidth
    case .request:
      title = "请求详情"
      width = Self.editorWidth
    }
    let subtitle: String
    if case .request(let id) = kind, let request = store.requests.first(where: { $0.id == id }) {
      subtitle = TrafficFormatting.logDate(request.at)
    } else {
      subtitle =
        AIUsageFormatting.date(store.interval.lowerBound) + " – "
        + AIUsageFormatting.date(store.interval.upperBound.addingTimeInterval(-1))
    }
    super.init(
      title: title,
      subtitle: subtitle,
      size: .init(width: width, height: PaxTheme.Routes.picker.height), style: .compact,
      symbol: "chart.bar")
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  private func observeInvalidation() {
    guard let data = store.data else { return }
    withObservationTracking {
      if data.invalidationVersion != invalidation {
        pane.clear()
        close()
        return
      }
    } onChange: { [weak self] in
      Task { @MainActor in self?.observeInvalidation() }
    }
  }
  override func loadView() {
    super.loadView()
    invalidation = store.data?.invalidationVersion ?? 0
    observeInvalidation()
    pane.setActions([])
    let list: [AIRequest]
    switch kind {
    case .model(let id): list = store.selected.filter { $0.modelID == id }
    case .request(let id): list = store.requests.filter { $0.id == id }
    default: list = store.selected
    }
    let s: AIUsageSummary
    if isRequest {
      s = AIUsageSummary(requests: list)
    } else if case .model(let id) = kind {
      s = store.usageSummary(model: id)
    } else {
      s = store.summary
    }
    switch kind {
    case .tokens:
      pane.add(
        statisticsChart(
          title: "总用量", total: AIUsageFormatting.tokens(s.tokens), unit: " Token",
          labels: ["普通输入", "缓存读取", "缓存写入", "输出"],
          values: [Double(s.ordinary), Double(s.read), Double(s.write), Double(s.output)],
          colors: PaxTheme.Chart.categorySeries,
          format: { AIUsageFormatting.tokens(Int($0)) }))
      pane.add(AIUsageLayout.separator())
      pane.add(
        fact("缓存命中率", AIUsageFormatting.percent(s.cache), color: PaxTheme.Color.text))
    case .requests:
      pane.add(
        statisticsChart(
          title: "总请求", total: "\(s.count)", unit: " 次", labels: ["成功", "失败"],
          values: [Double(s.success), Double(s.failed)],
          colors: PaxTheme.Chart.requestSeries,
          format: { String(Int($0)) + " 次" }))
      pane.add(AIUsageLayout.separator())
      pane.add(
        fact("成功率", AIUsageFormatting.percent(s.successRate), color: PaxTheme.Color.healthy)
      )
      if s.failed > 0 {
        pane.add(AIUsageLayout.heading("失败原因"))
        pane.add(fact("上游请求失败", "\(s.failed) 次", color: PaxTheme.Color.error))
      }
    case .cost:
      pane.add(
        statisticsChart(
          title: "总费用（官方）", total: s.officialCostLabel, unit: "",
          labels: ["普通输入", "缓存读取", "缓存写入", "输出"], values: s.officialCosts,
          colors: PaxTheme.Chart.categorySeries,
          format: { AIUsageFormatting.cost($0, precision: 4) }))
      pane.add(fact("渠道估算费用", s.costLabel))
      pane.add(AIUsageLayout.separator())
      profileRows(list, expanded: false, official: true)
    case .model, .request:
      pane.add(
        AIUsageLayout.equalRow([
          AIUsageLayout.stack([AIUsageLayout.heading("Token 构成"), tokenChart(s, stacked: true)]),
          AIUsageLayout.stack([
            AIUsageLayout.heading(isRequest ? "官方费用构成" : "费用构成"),
            costChart(s, stacked: true, official: isRequest),
          ]),
        ]))
      if isRequest {
        pane.add(
          fact(
            s.accountOnly ? "API 等值估算" : "官方费用（1×）", s.officialCostLabel,
            color: AIUsageCostDisplay.officialColor))
        if s.hasChannelEstimate {
          pane.add(fact("渠道费用", s.costLabel, color: AIUsageCostDisplay.channelColor))
        }
        if let request = list.first, request.hasChannelEstimate {
          pane.add(fact("请求记录倍率", AIUsageFormatting.multiplier(request.multiplier)))
        }
      }
      pane.add(AIUsageLayout.separator())
      let speed = isRequest ? list.first?.tps : s.mean(\.tps)
      pane.add(
        AIUsageLayout.equalRow([
          metric(
            isRequest ? "输出 TPS" : "平均输出 TPS",
            speed.map { String(format: "%.1f tok/s", $0) } ?? "—",
            color: isRequest
              ? PaxTheme.Color.healthy
              : speed.map {
                PaxTheme.RTT.endpoints(band: $0 >= 50 ? 0 : $0 >= 25 ? 1 : 2, role: .text).0
              }),
          metric(
            isRequest ? "总耗时" : "平均总耗时",
            (isRequest ? list.first?.elapsed : s.mean(\.elapsed)).map {
              String(format: "%.1fs", $0)
            } ?? "—"),
          metric("缓存命中率", AIUsageFormatting.percent(s.cache)),
        ]))
      pane.add(AIUsageLayout.label("总耗时 = 发送到结束；输出 TPS = 输出 Token ÷ 总耗时。", secondary: true))
      if !isRequest {
        let resultColors = PaxTheme.Chart.requestSeries
        let resultHeader = AIUsageLayout.stack(
          [
            AIUsageLayout.label("请求 \(s.count) 次"), NSView(),
            AIUsageLayout.label("成功率 " + AIUsageFormatting.percent(s.successRate), secondary: true),
          ], vertical: false)
        let resultBar = PaxShareBar(
          segments: [
            .init("成功", value: Double(s.success)), .init("失败", value: Double(s.failed)),
          ], colors: resultColors)
        let resultLegend = AIUsageLayout.stack(
          [
            PaxLegendDot(color: resultColors[0]),
            AIUsageLayout.label("成功 \(s.success)", secondary: true),
            PaxLegendDot(color: resultColors[1]),
            AIUsageLayout.label("失败 \(s.failed)", secondary: true),
            NSView(),
          ], vertical: false)
        pane.add(AIUsageLayout.stack([resultHeader, resultBar, resultLegend]))

      } else if let request = list.first, !request.succeeded {
        pane.add(AIUsageLayout.heading("失败信息"))
        pane.add(fact("原因", Self.failureMessage(request.errorMessage), color: PaxTheme.Color.error))
        pane.add(fact("错误码", request.errorMessage ?? "未提供"))
      }
      if isRequest, let request = list.first {
        pane.add(AIUsageLayout.separator())
        pane.add(fact("Profile", request.sourceLabel))
        pane.add(fact("CLI", request.client.rawValue))
        pane.add(fact("模型", store.model(request.modelID)))
        pane.add(fact("请求时间", TrafficFormatting.logDate(request.at)))
        pane.add(fact("请求 ID", request.id))
        pane.add(fact("推理强度", request.reasoningEffort.isEmpty ? "—" : request.reasoningEffort))
        if !request.reasoningEffort.isEmpty { pane.add(fact("配置来源", request.effortSource)) }
        pane.add(AIUsageLayout.label("缺少可靠发送时间的请求不显示耗时与 TPS。", secondary: true))
      } else {
        profileRows(list, expanded: true)
      }
      if isRequest {
        addPricing(list)
      } else {
        pane.add(AIUsageLayout.label("费用按官方分项价与请求当时的倍率汇总；价格表未覆盖的模型记 0。", secondary: true))
      }
    }
    if case .model(let id) = kind {
      pane.setActions([
        actions.button("查看模型日志", primary: true) { [weak self] in
          guard let self else { return }
          self.store.perform(.openLogs(model: id, profile: nil))
          self.close()
        }
      ])
    }
  }
  /// A single ring on the left and a compact percentage legend on the right.
  private func statisticsChart(
    title: String, total: String, unit: String, labels: [String], values: [Double],
    colors: [NSColor],
    format: (Double) -> String
  ) -> NSView {
    let sum = values.reduce(0, +)
    let chart = PaxDonutChart(
      segments: zip(labels, values).map { .init($0, value: $1) }, center: total, colors: colors)
    chart.toolTip = labels.indices.map {
      labels[$0] + " · " + format(values[$0]) + " · "
        + AIUsageFormatting.percent(sum > 0 ? values[$0] / sum : nil)
    }.joined(separator: "\n")
    let chartHolder = NSView()
    chart.translatesAutoresizingMaskIntoConstraints = false
    chartHolder.addSubview(chart)
    NSLayoutConstraint.activate([
      chart.centerXAnchor.constraint(equalTo: chartHolder.centerXAnchor),
      chart.centerYAnchor.constraint(equalTo: chartHolder.centerYAnchor),
      chart.widthAnchor.constraint(equalToConstant: PaxTheme.Chart.donutSize),
      chart.heightAnchor.constraint(equalTo: chart.widthAnchor),
      chartHolder.heightAnchor.constraint(equalTo: chart.heightAnchor),
      chartHolder.widthAnchor.constraint(equalTo: chart.widthAnchor),
    ])
    let legend: [NSView] = labels.indices.map { index in
      let dot = PaxLegendDot(color: colors[index])
      let row = AIUsageLayout.stack(
        [
          dot, AIUsageLayout.label(labels[index]), NSView(),
          AIUsageLayout.label(
            AIUsageFormatting.percent(sum > 0 ? values[index] / sum : nil), numeric: true),
        ], vertical: false)
      row.toolTip = format(values[index])
      return row
    }
    let heading = AIUsageLayout.stack(
      [
        AIUsageLayout.label(title, secondary: true), NSView(),
        AIUsageLayout.label(total + unit, numeric: true),
      ], vertical: false)
    let details = AIUsageLayout.stack([heading] + legend)
    let content = AIUsageLayout.stack([chartHolder, details], vertical: false)
    details.widthAnchor.constraint(
      equalTo: content.widthAnchor,
      constant: -PaxTheme.Chart.donutSize - content.spacing
    ).isActive = true
    return content
  }

  private func metric(_ name: String, _ value: String, color: NSColor? = nil) -> NSView {
    let label = AIUsageLayout.label(value, numeric: true)
    if let color { label.textColor = color }
    let stack = AIUsageLayout.stack([AIUsageLayout.label(name, secondary: true), label])
    stack.spacing = PaxTheme.Spacing.xs
    return stack
  }
  private func colored(_ value: String, _ color: NSColor) -> NSView {
    let label = AIUsageLayout.label(value, numeric: true)
    label.textColor = color
    return label
  }
  private func fact(_ name: String, _ value: String, color: NSColor? = nil) -> NSView {
    let label = AIUsageLayout.label(value, numeric: true)
    if let color { label.textColor = color }
    return AIUsageLayout.stack(
      [AIUsageLayout.label(name, secondary: true), NSView(), label], vertical: false)
  }
  private func tokenChart(_ s: AIUsageSummary, stacked: Bool = false) -> NSView {
    AIUsageLayout.composition(
      ["普通输入", "缓存读取", "缓存写入", "输出"],
      values: [Double(s.ordinary), Double(s.read), Double(s.write), Double(s.output)],
      center: AIUsageFormatting.tokens(s.tokens), stacked: stacked,
      format: { AIUsageFormatting.tokens(Int($0)) })
  }
  private func costChart(_ s: AIUsageSummary, stacked: Bool = false, official: Bool = false)
    -> NSView
  {
    return AIUsageLayout.composition(
      ["普通输入", "缓存读取", "缓存写入", "输出"], values: official ? s.officialCosts : s.costs,
      center: official ? s.officialCostLabel : s.costLabel,
      stacked: stacked, format: { AIUsageFormatting.cost($0, precision: 4) })
  }
  private func profileRows(_ list: [AIRequest], expanded: Bool, official: Bool = false) {
    pane.add(AIUsageLayout.heading("Profile 分布"))
    let modelID: String?
    if case .model(let id) = kind { modelID = id } else { modelID = nil }
    let all = store.usageSummary(model: modelID)
    let total = official ? all.originalCost : all.cost
    let ids =
      store.preview ? Set(list.map(\.profileID)) : Set(all.aggregateBuckets.map(\.profileID))
    for id in ids.sorted() {
      let rows = list.filter { $0.profileID == id }
      let s = store.usageSummary(model: modelID, profile: id)
      pane.add(
        AIUsageLayout.stack(
          [
            AIUsageLayout.label(
              rows.first?.profileName ?? all.aggregateBuckets.first { $0.profileID == id }?
                .profileName ?? id), NSView(),
            AIUsageLayout.label(AIUsageFormatting.tokens(s.tokens) + " Token", numeric: true),
            colored(official ? s.officialCostLabel : s.costLabel, PaxTheme.Color.text),
          ], vertical: false))
      if expanded {
        pane.add(
          PaxShareBar(
            segments: [.init(id, value: official ? s.originalCost : s.cost)],
            colors: [PaxTheme.Routes.blue],
            striped: [0], total: total))
        pane.add(
          AIUsageLayout.stack(
            [
              AIUsageLayout.label(
                s.timingText(\.tps, "%.1f tok/s") ?? "—", secondary: true),
              AIUsageLayout.label(
                s.timingText(\.elapsed, "平均 %.1fs") ?? "—", secondary: true),
              AIUsageLayout.label("缓存 " + AIUsageFormatting.percent(s.cache), secondary: true),
              NSView(),
              actions.button("查看日志") { [weak self] in
                guard let self else { return }
                guard let modelID = modelID ?? rows.first?.modelID else { return }
                self.store.perform(.openLogs(model: modelID, profile: id))
                self.close()
              },
            ], vertical: false))
      }
    }
  }
  private func addPricing(_ list: [AIRequest]) {
    let s = AIUsageSummary(requests: list)
    func prices(_ values: [Double?]) -> String {
      guard !values.isEmpty, values.allSatisfy({ $0 != nil }) else { return "—" }
      return Set(values.compactMap { $0 }).sorted().map { AIUsageFormatting.cost($0, precision: 4) }
        .joined(separator: " / ")
    }
    let r = list.first
    var fields: [NSView] = []
    let titles = ["普通输入单价 / 1M", "缓存读取单价 / 1M", "缓存写入加权单价 / 1M", "输出单价 / 1M"]
    for i in 0..<4 {
      let unit: Double? = r.flatMap { item in
        if item.componentPrices.count == 4 { return item.componentPrices[i] }
        return i == 0 ? item.inputPrice : i == 3 ? item.outputPrice : nil
      }
      fields.append(fact(titles[i], prices([unit]), color: PaxTheme.Color.text))
    }
    if let r {
      if let short = r.cacheWriteShort, let long = r.cacheWriteLong {
        let labels = r.client == .claude ? ["5分钟", "1小时"] : ["30分钟", "12小时"]
        fields.append(
          fact(
            "缓存写入 " + labels[0] + " / " + labels[1],
            prices([r.cacheWriteShortPrice]) + " / " + prices([r.cacheWriteLongPrice])))
        fields.append(fact("缓存写入短期 / 长期Token", "\(short) / \(long)"))
      }
      if r.hasChannelEstimate {
        fields.append(fact("渠道倍率", AIUsageFormatting.multiplier(r.multiplier)))
      }
      fields.append(fact("官方价格版本", r.priceVersion.isEmpty ? "预览样本" : r.priceVersion))
    }
    fields.append(
      fact(
        s.accountOnly ? "API 等值估算" : "官方费用（1×）",
        AIUsageFormatting.cost(s.originalCost, precision: 6),
        color: AIUsageCostDisplay.officialColor))
    if s.hasChannelEstimate {
      fields.append(fact("渠道费用", s.costLabel, color: AIUsageCostDisplay.channelColor))
    }
    fields.append(AIUsageLayout.label(AIUsageCostDisplay.explanation, secondary: true))
    let facts = AIUsageLayout.stack(fields)
    facts.isHidden = true
    let toggle = actions.button("计费依据  ·  历史单价与倍率  ›") { [weak self, weak facts] in
      facts?.isHidden.toggle()
      self?.pane.invalidateContentSize()
    }
    toggle.compactStyle = .text
    toggle.leadingAligned = true
    pane.add(AIUsageLayout.separator())
    pane.add(toggle)
    pane.add(facts)
  }
  private static func failureMessage(_ code: String?) -> String {
    switch code {
    case "AUTH", "http_401", "http_403": return "认证失败或没有访问权限"
    case "RATE_LIMIT", "http_429": return "请求超过服务限流"
    case "TIMEOUT": return "请求超时"
    case "http_402": return "服务拒绝计费或额度不足"
    case "TRANSPORT": return "服务连接失败"
    default: return "上游请求失败，日志没有更详细的安全说明"
    }
  }

}
