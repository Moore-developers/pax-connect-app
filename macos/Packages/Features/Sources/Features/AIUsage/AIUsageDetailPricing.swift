import AppKit
import PaxCore
import PaxUI

extension AIUsageDetailController {
  func profileRows(_ list: [AIRequest], expanded: Bool, official: Bool = false) {
    pane.add(self.interfaceCopy.usageHeading("action.usage.profile_distribution"))
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
            AIUsageLayout.label(
              AIUsageFormatting.tokens(s.tokens)
                + interfaceCopy.text("ai_usage.detail.token_suffix"),
              numeric: true),
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
                s.timingText(\.elapsed, "%.1fs").map {
                  interfaceCopy.text("ai_usage.detail.average_duration", ["duration": $0])
                } ?? "—", secondary: true),
              AIUsageLayout.label(
                self.interfaceCopy.text("interface_detail.32c7a9c133")
                  + AIUsageFormatting.percent(s.cache), secondary: true),
              NSView(),
              actions.button(self.interfaceCopy.text("onboarding.view_logs")) { [weak self] in
                guard let self else { return }
                guard let modelID = modelID ?? rows.first?.modelID else { return }
                self.store.perform(.openLogs(model: modelID, profile: id))
                self.close()
              },
            ], vertical: false))
      }
    }
  }
  func addPricing(_ list: [AIRequest]) {
    let s = AIUsageSummary(requests: list)
    func prices(_ values: [Double?]) -> String {
      guard !values.isEmpty, values.allSatisfy({ $0 != nil }) else { return "—" }
      return Set(values.compactMap { $0 }).sorted().map { AIUsageFormatting.cost($0, precision: 4) }
        .joined(separator: " / ")
    }
    let r = list.first
    var fields: [NSView] = []
    let titles = [
      self.interfaceCopy.text("interface_detail.57b7d7f402"),
      self.interfaceCopy.text("interface_detail.5188cef9c4"),
      self.interfaceCopy.text("interface_detail.e8b3aff898"),
      self.interfaceCopy.text("interface_detail.4cc47d3034"),
    ]
    for i in 0..<4 {
      let unit: Double? = r.flatMap { item in
        if item.componentPrices.count == 4 { return item.componentPrices[i] }
        return i == 0 ? item.inputPrice : i == 3 ? item.outputPrice : nil
      }
      fields.append(fact(titles[i], prices([unit]), color: PaxTheme.Color.text))
    }
    if let r {
      if let short = r.cacheWriteShort, let long = r.cacheWriteLong {
        let labels =
          r.client == .claude
          ? [
            self.interfaceCopy.text("interface_detail.6f5eeb3f95"),
            self.interfaceCopy.text("settings.logs.one_hour"),
          ]
          : [
            self.interfaceCopy.text("logs.thirty_minutes"),
            self.interfaceCopy.text("settings.logs.twelve_hours"),
          ]
        fields.append(
          fact(
            self.interfaceCopy.text("interface_detail.604b17222a") + labels[0] + " / " + labels[1],
            prices([r.cacheWriteShortPrice]) + " / " + prices([r.cacheWriteLongPrice])))
        fields.append(
          fact(self.interfaceCopy.text("interface_detail.149d5ff0ba"), "\(short) / \(long)"))
      }
      if r.hasChannelEstimate {
        fields.append(
          fact(
            self.interfaceCopy.text("interface_detail.ffa753ff44"),
            AIUsageFormatting.multiplier(r.multiplier)))
      }
      fields.append(
        fact(
          self.interfaceCopy.text("interface_detail.b29e34d706"),
          r.priceVersion.isEmpty
            ? self.interfaceCopy.text("interface_detail.a4ca1a62b0") : r.priceVersion))
    }
    fields.append(
      fact(
        s.accountOnly
          ? self.interfaceCopy.text("interface_detail.0bd0fec783")
          : self.interfaceCopy.text("interface_detail.a68c3521af"),
        AIUsageFormatting.cost(s.originalCost, precision: 6),
        color: AIUsageCostDisplay.officialColor))
    if s.hasChannelEstimate {
      fields.append(
        fact(
          self.interfaceCopy.text("session_limits.4d14ea51f5"), s.costLabel,
          color: AIUsageCostDisplay.channelColor))
    }
    let note = interfaceCopy.usageLabel("action.usage.billing_summary", secondary: true)
    note.toolTip = AIUsageCostDisplay.explanation(context: interfaceCopy.context)
    fields.append(note)
    let facts = AIUsageLayout.stack(fields)
    facts.isHidden = true
    let toggle = actions.button(self.interfaceCopy.text("action.usage.billing_basis")) {
      [weak self, weak facts] in
      facts?.isHidden.toggle()
      self?.pane.invalidateContentSize()
    }
    toggle.compactStyle = .text
    toggle.leadingAligned = true
    pane.add(AIUsageLayout.separator())
    pane.add(toggle)
    pane.add(facts)
  }
}
