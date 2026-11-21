import AppKit
import PaxClient
import PaxCore
import PaxUI

/// Preview-then-confirm controls for repricing history. Built once by the editor and refreshed
/// in place, so the chosen scope and range survive the editor's re-renders.
@MainActor
final class AIRepriceControl: NSObject {
  let view = AIUsageLayout.stack([])
  let flow: AIRepriceFlow
  /// A non-nil reason disables the actions and is shown as the tooltip and a caption.
  var blockReason: () -> String? = { nil }
  var onDismiss: (() -> Void)?
  var onLayoutChange: (() -> Void)?
  private let context: PresentationContext
  private let profileID: String
  private let todayOnly: Bool
  private let now: () -> Date
  private let calendar: Calendar
  private let actions = AIUsageActions()
  let scope = PaxRadioGroup(["整个 Profile", "指定模型"])
  private let model = NSPopUpButton(frame: .zero, pullsDown: false)
  let window = PaxRadioGroup(["今天", "本周", "自定义日期", "全部历史"])
  private lazy var dateButton = PaxCompactButton("", target: self, action: #selector(openDates))
  private var from: Date
  private var to: Date
  private var datePopover: NSPopover?
  private let choices = AIUsageLayout.stack([])
  private let status = AIRepriceControl.wrapped()
  private let note = AIRepriceControl.wrapped()
  private let buttons = AIUsageLayout.stack([], vertical: false)
  private var models: [String] = []

  /// `todayOnly` is the post-save shortcut: whole profile, today, no pickers.
  init(
    profileID: String, flow: AIRepriceFlow, todayOnly: Bool = false,
    now: @escaping () -> Date = Date.init, calendar: Calendar = .current,
    context: PresentationContext = .chinese
  ) {
    self.context = context
    self.profileID = profileID
    self.flow = flow
    self.todayOnly = todayOnly
    self.now = now
    self.calendar = calendar
    from = now()
    to = now()
    super.init()
    scope.apply(titles: [
      context.text("reprice.entire_profile"), context.text("reprice.specific_model"),
    ])
    window.apply(titles: [
      context.text("reprice.today"), context.text("reprice.week"),
      context.text("reprice.custom_dates"), context.text("reprice.all_history"),
    ])
    dateButton.image = PaxRouteIcon.image("calendar")
    dateButton.trailingImage = PaxIcon.image("chevron.down", size: PaxTheme.DateRange.chevronSize)
    dateButton.setAccessibilityLabel(self.context.text("reprice.select_dates"))
    let scopeRow = AIUsageLayout.stack(
      [
        AIUsageLayout.label(self.context.text("reprice.scope"), secondary: true), scope, model,
        NSView(),
      ], vertical: false)
    let timeRow = AIUsageLayout.stack(
      [
        AIUsageLayout.label(self.context.text("reprice.time"), secondary: true), window, dateButton,
        NSView(),
      ], vertical: false)
    for row in [scopeRow, timeRow] {
      choices.addArrangedSubview(row)
      // The date button only shows for 自定义日期; a fixed row height keeps the card from jumping.
      row.heightAnchor.constraint(equalToConstant: PaxTheme.Control.compactHeight).isActive = true
    }
    scope.setAccessibilityIdentifier("ai-reprice-scope")
    window.setAccessibilityIdentifier("ai-reprice-window")
    scope.setAccessibilityLabel(self.context.text("reprice.scope_ax"))
    model.setAccessibilityLabel(self.context.text("reprice.specific_model"))
    window.setAccessibilityLabel(self.context.text("reprice.time_ax"))
    for group in [scope, window] { group.onSelect = { [weak self] _ in self?.inputChanged() } }
    model.target = self
    model.action = #selector(inputChanged)
    note.textColor = PaxTheme.Color.warning
    model.widthAnchor.constraint(lessThanOrEqualToConstant: 240).isActive = true
    view.spacing = PaxTheme.Spacing.medium
    for part in todayOnly ? [status, note, buttons] : [choices, status, note, buttons] {
      view.addArrangedSubview(part)
    }
    flow.onChange = { [weak self] in self?.refresh() }
    refresh()
  }

  private static func wrapped() -> NSTextField {
    PaxText.label(
      "", font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.captionFont), secondary: true,
      wrapping: true)
  }

  func setModels(_ ids: [String]) {
    guard ids != models else { return }
    let keep = model.titleOfSelectedItem
    models = ids
    model.removeAllItems()
    model.addItems(withTitles: ids)
    if let keep, ids.contains(keep) { model.selectItem(withTitle: keep) }
    refresh()
  }

  /// The request the current inputs stand for; nil when they are incomplete or invalid.
  var request: AIRepriceFlow.Request? {
    if todayOnly {
      return .init(profileID: profileID, model: nil, range: AIReprice.range(.today, now: now()))
    }
    let pickedModel = scope.selectedIndex == 1 ? model.titleOfSelectedItem : nil
    if scope.selectedIndex == 1, pickedModel == nil { return nil }
    let choice: AIRepriceWindow
    switch window.selectedIndex {
    case 0: choice = .today
    case 1: choice = .week
    case 2: choice = .custom(from, to)
    default: choice = .all
    }
    if case .custom(let start, let end) = choice,
      calendar.startOfDay(for: start) > calendar.startOfDay(for: end)
    {
      return nil
    }
    return .init(
      profileID: profileID, model: pickedModel,
      range: AIReprice.range(choice, now: now(), calendar: calendar))
  }

  @objc private func openDates() {
    // 时间下拉已有今日/本周/全部，日期弹层不再放预设。
    let picker = PaxDateRangePicker(
      start: from, end: to, calendar: calendar, now: now(),
      options: .init(presets: [], maxDate: now()), copy: dateRangeCopy(context))
    let popover = NSPopover()
    popover.behavior = .transient
    popover.applyMotionPreference()
    popover.contentViewController = picker
    picker.onApply = { [weak self, weak popover] range in
      self?.from = range.start
      self?.to = range.end
      popover?.close()
      self?.inputChanged()
      return nil
    }
    picker.onCancel = { [weak popover] in popover?.close() }
    datePopover = popover
    popover.show(relativeTo: dateButton.bounds, of: dateButton, preferredEdge: .maxY)
  }

  @objc private func inputChanged() {
    flow.reset()
    refresh()
  }

  /// Re-reads the flow state and the editor's block reason.
  func refresh() {
    let custom = window.selectedIndex == 2
    scope.isHidden = todayOnly
    model.isHidden = scope.selectedIndex != 1
    dateButton.isHidden = !custom
    dateButton.title = AIUsageFormatting.date(from)
    if !calendar.isDate(from, inSameDayAs: to) {
      dateButton.title += " – " + AIUsageFormatting.date(to)
    }
    choices.isHidden = todayOnly
    let blocked =
      blockReason() ?? (flow.available ? nil : self.context.text("reprice.demo_unavailable"))
    var reason = blocked
    if reason == nil, request == nil {
      reason =
        scope.selectedIndex == 1 && model.titleOfSelectedItem == nil
        ? self.context.text("reprice.no_models") : self.context.text("reprice.invalid_dates")
    }
    if blocked != nil, case .preview = flow.state {
      flow.reset()  // the preview no longer matches what would be saved
      return
    }
    status.textColor = PaxTheme.Color.textSecondary
    note.stringValue = ""
    let working = flow.state == .working
    switch flow.state {
    case .idle: status.stringValue = reason ?? ""
    case .working: status.stringValue = self.context.text("reprice.working")
    case .preview(let result):
      status.stringValue = AIReprice.previewText(result, context: context)
      if result.requests > 0 {
        note.stringValue = self.context.text("reprice.confirmation_note")
      }
    case .done(let result):
      status.stringValue = AIReprice.doneText(result, context: context)
    case .failed(let error):
      status.stringValue = AIReprice.message(error, context: context)
      status.textColor = PaxTheme.Color.error
    }
    // Empty captions would still reserve a line plus stack spacing, so collapse them.
    status.isHidden = status.stringValue.isEmpty
    note.isHidden = note.stringValue.isEmpty
    scope.isEnabled = !working
    window.isEnabled = !working
    for control in [model, dateButton] as [NSControl] { control.isEnabled = !working }
    rebuildButtons(reason: reason, working: working)
    onLayoutChange?()
  }

  private func rebuildButtons(reason: String?, working: Bool) {
    for item in buttons.arrangedSubviews {
      buttons.removeArrangedSubview(item)
      item.removeFromSuperview()
    }
    actions.reset()
    var items: [NSView] = []
    switch flow.state {
    case .preview(let result):
      items.append(
        actions.button(
          self.context.text("reprice.confirm"), primary: true,
          enabled: result.requests > 0 && !working, id: "ai-reprice-confirm"
        ) { [weak self] in
          guard let self else { return }
          Task { await self.flow.confirm() }
        })
      items.append(
        actions.button(self.context.text("reprice.cancel"), id: "ai-reprice-cancel") {
          [weak self] in self?.cancel()
        })
    case .done:
      items.append(
        actions.button(self.context.text("reprice.finish"), id: "ai-reprice-finish") {
          [weak self] in self?.cancel()
        })
    default:
      let title =
        todayOnly
        ? self.context.text("reprice.update_today") : self.context.text("reprice.preview_action")
      let go = actions.button(
        working ? self.context.text("reprice.working_action") : title, primary: todayOnly,
        enabled: reason == nil && !working, id: "ai-reprice-preview"
      ) { [weak self] in
        guard let self, let request = self.request else { return }
        Task { await self.flow.preview(request) }
      }
      go.toolTip = reason
      if !todayOnly { go.capsuleTone = .graphite }
      items.append(go)
      if todayOnly {
        items.append(
          actions.button(self.context.text("reprice.later"), id: "ai-reprice-dismiss") {
            [weak self] in self?.onDismiss?()
          })
      }
    }
    items.append(NSView())
    for item in items { buttons.addArrangedSubview(item) }
  }

  private func cancel() {
    flow.reset()
    if todayOnly { onDismiss?() }
  }
}
