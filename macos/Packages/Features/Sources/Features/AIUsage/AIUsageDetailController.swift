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
  let store: AIUsageStore
  let kind: Kind
  private var invalidation: UInt64 = 0
  let actions = AIUsageActions()
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
    let width: CGFloat
    switch kind {
    case .cost:
      width = Self.statisticsWidth
    case .tokens:
      width = Self.statisticsWidth
    case .requests:
      width = Self.statisticsWidth
    case .model:
      width = Self.inspectorWidth
    case .request:
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
      title: "",
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
    let title: String
    switch kind {
    case .cost: title = interfaceCopy.text("ai_projection.dd4e402aba")
    case .tokens: title = interfaceCopy.text("ai_usage.detail.tokens")
    case .requests: title = interfaceCopy.text("ai_usage.detail.requests")
    case .model(let id):
      title = interfaceCopy.text("ai_usage.detail.model", ["model": store.model(id)])
    case .request: title = interfaceCopy.text("ai_usage.detail.request")
    }
    pane.applyTitle(title)
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
          title: self.interfaceCopy.text("interface_detail.e506bdf346"),
          total: AIUsageFormatting.tokens(s.tokens),
          unit: interfaceCopy.text("ai_usage.detail.token_suffix"),
          labels: [
            self.interfaceCopy.text("session_limits.faa58c14d8"),
            self.interfaceCopy.text("session_limits.1ded6559f6"),
            self.interfaceCopy.text("session_limits.6078a681a3"),
            self.interfaceCopy.text("dashboard_value.output"),
          ],
          values: [Double(s.ordinary), Double(s.read), Double(s.write), Double(s.output)],
          colors: PaxTheme.Chart.categorySeries,
          format: { AIUsageFormatting.tokens(Int($0)) }))
      pane.add(AIUsageLayout.separator())
      pane.add(
        fact(
          self.interfaceCopy.text("interface_detail.13b61c5f69"),
          AIUsageFormatting.percent(s.cache), color: PaxTheme.Color.text))
    case .requests:
      pane.add(
        statisticsChart(
          title: self.interfaceCopy.text("interface_detail.ddde16f883"), total: "\(s.count)",
          unit: self.interfaceCopy.text("dashboard_value.request_suffix"),
          labels: [
            self.interfaceCopy.text("dashboard_value.success"),
            self.interfaceCopy.text("event.result.failed"),
          ],
          values: [Double(s.success), Double(s.failed)],
          colors: PaxTheme.Chart.requestSeries,
          format: { String(Int($0)) + self.interfaceCopy.text("dashboard_value.request_suffix") }))
      pane.add(AIUsageLayout.separator())
      pane.add(
        fact(
          self.interfaceCopy.text("ai_usage.success_rate"),
          AIUsageFormatting.percent(s.successRate), color: PaxTheme.Color.healthy)
      )
      if s.failed > 0 {
        pane.add(self.interfaceCopy.usageHeading("action.usage.failure_reason"))
        pane.add(
          fact(
            self.interfaceCopy.text("interface_detail.088b70840a"),
            self.interfaceCopy.text(
              "interface_detail.8cc9b62351", ["arg0": String(describing: s.failed)]),
            color: PaxTheme.Color.error))
      }
    case .cost:
      pane.add(
        statisticsChart(
          title: self.interfaceCopy.text("interface_detail.e9e3f1858d"), total: s.officialCostLabel,
          unit: "",
          labels: [
            self.interfaceCopy.text("session_limits.faa58c14d8"),
            self.interfaceCopy.text("session_limits.1ded6559f6"),
            self.interfaceCopy.text("session_limits.6078a681a3"),
            self.interfaceCopy.text("dashboard_value.output"),
          ], values: s.officialCosts,
          colors: PaxTheme.Chart.categorySeries,
          format: { AIUsageFormatting.cost($0, precision: 4) }))
      pane.add(fact(self.interfaceCopy.text("interface_detail.2d26875b04"), s.costLabel))
      pane.add(AIUsageLayout.separator())
      profileRows(list, expanded: false, official: true)
    case .model, .request:
      pane.add(
        AIUsageLayout.equalRow([
          AIUsageLayout.stack([
            self.interfaceCopy.usageHeading("action.usage.token_breakdown"),
            tokenChart(s, stacked: true),
          ]),
          AIUsageLayout.stack([
            AIUsageLayout.heading(
              isRequest
                ? self.interfaceCopy.text("interface_detail.65120c457d")
                : self.interfaceCopy.text("interface_detail.0b72cfc3ac")),
            costChart(s, stacked: true, official: isRequest),
          ]),
        ]))
      if isRequest {
        pane.add(
          fact(
            s.accountOnly
              ? self.interfaceCopy.text("interface_detail.0bd0fec783")
              : self.interfaceCopy.text("interface_detail.a68c3521af"), s.officialCostLabel,
            color: AIUsageCostDisplay.officialColor))
        if s.hasChannelEstimate {
          pane.add(
            fact(
              self.interfaceCopy.text("session_limits.4d14ea51f5"), s.costLabel,
              color: AIUsageCostDisplay.channelColor))
        }
        if let request = list.first, request.hasChannelEstimate {
          pane.add(
            fact(
              self.interfaceCopy.text("interface_detail.1b13fb3ed3"),
              AIUsageFormatting.multiplier(request.multiplier)))
        }
      }
      pane.add(AIUsageLayout.separator())
      let speed = isRequest ? list.first?.tps : s.mean(\.tps)
      pane.add(
        AIUsageLayout.equalRow([
          metric(
            isRequest
              ? self.interfaceCopy.text("ai_usage.output_tps")
              : self.interfaceCopy.text("interface_detail.cb7a549d50"),
            speed.map { String(format: "%.1f tok/s", $0) } ?? "—",
            color: isRequest
              ? PaxTheme.Color.healthy
              : speed.map {
                PaxTheme.RTT.endpoints(band: $0 >= 50 ? 0 : $0 >= 25 ? 1 : 2, role: .text).0
              }),
          metric(
            isRequest
              ? self.interfaceCopy.text("interface_detail.42aa8e01e9")
              : self.interfaceCopy.text("interface_detail.c111608af2"),
            (isRequest ? list.first?.elapsed : s.mean(\.elapsed)).map {
              String(format: "%.1fs", $0)
            } ?? "—"),
          metric(
            self.interfaceCopy.text("interface_detail.13b61c5f69"),
            AIUsageFormatting.percent(s.cache)),
        ]))
      pane.add(self.interfaceCopy.usageLabel("action.usage.timing_note", secondary: true))
      if !isRequest {
        let resultColors = PaxTheme.Chart.requestSeries
        let resultHeader = AIUsageLayout.stack(
          [
            AIUsageLayout.label(
              self.interfaceCopy.text(
                "interface_detail.db74e7a8c2", ["arg0": String(describing: s.count)])), NSView(),
            AIUsageLayout.label(
              self.interfaceCopy.text("interface_detail.1357d7666f")
                + AIUsageFormatting.percent(s.successRate), secondary: true),
          ], vertical: false)
        let resultBar = PaxShareBar(
          segments: [
            .init(self.interfaceCopy.text("dashboard_value.success"), value: Double(s.success)),
            .init(self.interfaceCopy.text("event.result.failed"), value: Double(s.failed)),
          ], colors: resultColors)
        let resultLegend = AIUsageLayout.stack(
          [
            PaxLegendDot(color: resultColors[0]),
            AIUsageLayout.label(
              self.interfaceCopy.text(
                "interface_detail.4afe849f28", ["arg0": String(describing: s.success)]),
              secondary: true),
            PaxLegendDot(color: resultColors[1]),
            AIUsageLayout.label(
              self.interfaceCopy.text(
                "interface_detail.7a4339ce33", ["arg0": String(describing: s.failed)]),
              secondary: true),
            NSView(),
          ], vertical: false)
        pane.add(AIUsageLayout.stack([resultHeader, resultBar, resultLegend]))

      } else if let request = list.first, !request.succeeded {
        pane.add(self.interfaceCopy.usageHeading("action.usage.failure_information"))
        pane.add(
          fact(
            self.interfaceCopy.text("interface_detail.f029d9cb4a"),
            failureMessage(request.errorMessage), color: PaxTheme.Color.error))
        pane.add(
          fact(
            self.interfaceCopy.text("interface_detail.9c7ff3321e"),
            request.errorMessage ?? self.interfaceCopy.text("diagnostic_evidence.756762e293")))
      }
      if isRequest, let request = list.first {
        pane.add(AIUsageLayout.separator())
        pane.add(fact("Profile", request.sourceLabel(context: interfaceCopy.context)))
        pane.add(fact("CLI", request.client.rawValue))
        pane.add(fact(self.interfaceCopy.text("session.kind.models"), store.model(request.modelID)))
        pane.add(
          fact(
            self.interfaceCopy.text("interface_detail.444e4ac452"),
            TrafficFormatting.logDate(request.at)))
        pane.add(fact(self.interfaceCopy.text("interface_detail.12da486580"), request.id))
        pane.add(
          fact(
            self.interfaceCopy.text("interface_detail.c8c14507b2"),
            request.reasoningEffort.isEmpty ? "—" : request.reasoningEffort))
        if !request.reasoningEffort.isEmpty {
          pane.add(
            fact(self.interfaceCopy.text("interface_detail.1fa1a10b63"), request.effortSource))
        }
        pane.add(self.interfaceCopy.usageLabel("action.usage.missing_timing", secondary: true))
      } else {
        profileRows(list, expanded: true)
      }
      if isRequest {
        addPricing(list)
      } else {
        pane.add(self.interfaceCopy.usageLabel("action.usage.pricing_note", secondary: true))
      }
    }
    if case .model(let id) = kind {
      pane.setActions([
        actions.button(self.interfaceCopy.text("action.usage.view_model_logs"), primary: true) {
          [weak self] in
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
  func colored(_ value: String, _ color: NSColor) -> NSView {
    let label = AIUsageLayout.label(value, numeric: true)
    label.textColor = color
    return label
  }
  func fact(_ name: String, _ value: String, color: NSColor? = nil) -> NSView {
    let label = AIUsageLayout.label(value, numeric: true)
    if let color { label.textColor = color }
    return AIUsageLayout.stack(
      [AIUsageLayout.label(name, secondary: true), NSView(), label], vertical: false)
  }
  private func tokenChart(_ s: AIUsageSummary, stacked: Bool = false) -> NSView {
    AIUsageLayout.composition(
      [
        self.interfaceCopy.text("session_limits.faa58c14d8"),
        self.interfaceCopy.text("session_limits.1ded6559f6"),
        self.interfaceCopy.text("session_limits.6078a681a3"),
        self.interfaceCopy.text("dashboard_value.output"),
      ],
      values: [Double(s.ordinary), Double(s.read), Double(s.write), Double(s.output)],
      center: AIUsageFormatting.tokens(s.tokens), stacked: stacked,
      format: { AIUsageFormatting.tokens(Int($0)) })
  }
  private func costChart(_ s: AIUsageSummary, stacked: Bool = false, official: Bool = false)
    -> NSView
  {
    return AIUsageLayout.composition(
      [
        self.interfaceCopy.text("session_limits.faa58c14d8"),
        self.interfaceCopy.text("session_limits.1ded6559f6"),
        self.interfaceCopy.text("session_limits.6078a681a3"),
        self.interfaceCopy.text("dashboard_value.output"),
      ], values: official ? s.officialCosts : s.costs,
      center: official ? s.officialCostLabel : s.costLabel,
      stacked: stacked, format: { AIUsageFormatting.cost($0, precision: 4) })
  }
  private func failureMessage(_ code: String?) -> String {
    switch code {
    case "AUTH", "http_401", "http_403": return interfaceCopy.text("ai_usage.detail.auth_failed")
    case "RATE_LIMIT", "http_429": return interfaceCopy.text("ai_usage.detail.rate_limit")
    case "TIMEOUT": return interfaceCopy.text("ai_usage.detail.timeout")
    case "http_402": return interfaceCopy.text("ai_usage.detail.billing_failed")
    case "TRANSPORT": return interfaceCopy.text("ai_usage.detail.connection_failed")
    default: return interfaceCopy.text("ai_usage.detail.upstream_failed")
    }
  }

}
