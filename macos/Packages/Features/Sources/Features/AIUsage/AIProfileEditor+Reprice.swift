import AppKit
import PaxCore
import PaxUI

/// Multiplier drafts, the 模型倍率 / 重算历史费用 sections and the post-save 同步更新今日数据 banner.
extension AIProfileEditor {
  var defaultInput: AIMultiplierInput {
    let text = multiplier?.stringValue ?? ""
    // Untouched field: keep the stored value even if it has more decimals than the display.
    if let saved = editBaseline?.multiplier, text == AIUsageFormatting.multiplierInput(saved) {
      return .value(saved)
    }
    return AIMultiplierInput.parse(text)
  }
  var inputsValid: Bool {
    if case .value = defaultInput { return rates.isValid }
    return false
  }
  /// Unsaved multiplier edits (default or any model); history updates wait for a save.
  var multiplierDraftChanged: Bool {
    defaultInput != .value(editBaseline?.multiplier ?? 1)
      || rates.hasChanges(from: editBaseline?.modelMultipliers ?? [:])
  }
  var repriceBlockReason: String? { multiplierDraftChanged ? "请先保存倍率修改，再更新历史" : nil }

  func draftChanged() { syncDraftState() }

  /// Save enablement, the default-multiplier error, placeholders and reprice buttons follow the drafts.
  func syncDraftState() {
    saveButton?.isEnabled = !saving && inputsValid
    if case .invalid = defaultInput {
      pane.status.stringValue = "计费倍率：" + AIMultiplierInput.errorText
      pane.status.textColor = PaxTheme.Color.error
      pane.status.isHidden = false
      pane.status.superview?.isHidden = false
      multiplierErrorShown = true
    } else if multiplierErrorShown {
      multiplierErrorShown = false
      if pane.status.stringValue.hasPrefix("计费倍率：") {  // leave unrelated messages alone
        pane.status.stringValue = ""
        pane.status.isHidden = true
      }
    }
    if case .value(let rate) = defaultInput {
      rates.update(models: rates.models, fallback: rate)
    }
    repriceControl?.refresh()
    todayControl?.refresh()
  }

  /// The model ids offered for per-model multipliers and for 指定模型.
  func rateModels(_ p: AIProfile) -> [String] {
    let counts = store.modelUsage(profile: p.id, lifetime: true).mapValues(\.count)
    return AIModelRateList.models(
      catalog: p.catalog.map(\.id), counts: counts, overrides: p.modelMultipliers)
  }

  /// 模型倍率 and 重算历史费用; built once so drafts and the chosen range survive re-renders.
  func profileRateSections(_ p: AIProfile) -> [NSView] {
    let models = rateModels(p)
    if ratesCard == nil {
      ratesCard = profileSection(
        "模型倍率", symbol: "percent",
        views: [
          AIUsageLayout.label("留空则使用默认倍率（「配置」页的计费倍率）；保存后只影响之后的新请求。", secondary: true),
          rates.view,
        ])
      let control = AIRepriceControl(
        profileID: p.id, flow: AIRepriceFlow(data: store.data), now: { [store] in store.now },
        calendar: store.calendar)
      control.blockReason = { [weak self] in self?.repriceBlockReason }
      control.onLayoutChange = { [weak self] in self?.pane.invalidateContentSize() }
      repriceControl = control
      repriceCard = profileSection(
        "重算历史费用", symbol: "arrow.triangle.2.circlepath",
        views: [
          AIUsageLayout.label("用已保存的倍率，重新计算过去请求的费用。会改写已记录的费用，无法撤销。", secondary: true),
          control.view,
        ])
    }
    let fallback: Double
    if case .value(let rate) = defaultInput { fallback = rate } else { fallback = p.multiplier }
    rates.update(models: models, fallback: fallback)
    repriceControl?.setModels(models)
    return [ratesCard, repriceCard].compactMap { $0 }
  }

  /// A save that changed multipliers keeps the editor open with the baseline moved to the saved
  /// profile, and offers to bring today's data in line.
  func saved(_ latest: AIProfile) {
    apiKey?.stringValue = ""
    markSaved(latest)
    repriceControl?.flow.reset()
    let control = AIRepriceControl(
      profileID: latest.id, flow: AIRepriceFlow(data: store.data), todayOnly: true,
      now: { [store] in store.now }, calendar: store.calendar)
    control.blockReason = { [weak self] in self?.repriceBlockReason }
    control.onLayoutChange = { [weak self] in self?.pane.invalidateContentSize() }
    control.onDismiss = { [weak self] in
      self?.todayBanner = nil
      self?.todayControl = nil
      self?.render()
    }
    todayControl = control
    todayBanner = profileSection(
      "倍率已保存", symbol: "checkmark.circle",
      views: [AIUsageLayout.label("历史费用仍按原倍率；可只把今日数据按新倍率更新。", secondary: true), control.view])
    render()
  }

  /// Opens at the requested section (一次, after the first layout).
  func scrollToRequestedSection() {
    guard !scrolled, scrollTarget == .reprice, let card = repriceCard,
      let document = pane.bodyScroll.documentView, card.frame.height > 0
    else { return }
    scrolled = true
    let clip = pane.bodyScroll.contentView.bounds.height
    let rect = card.convert(card.bounds, to: document)
    let y = document.isFlipped ? rect.minY : rect.maxY - clip
    let limit = max(0, document.frame.height - clip)
    pane.bodyScroll.contentView.scroll(to: NSPoint(x: 0, y: min(max(0, y), limit)))
    pane.bodyScroll.reflectScrolledClipView(pane.bodyScroll.contentView)
  }
}
