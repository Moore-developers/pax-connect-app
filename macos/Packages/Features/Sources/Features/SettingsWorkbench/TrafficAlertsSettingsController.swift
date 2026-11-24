import AppKit
import Observation
import PaxCore
import PaxUI

@MainActor
final class TrafficAlertsSettingsController: NSViewController, InterfaceLanguageConsumer,
  NSTextFieldDelegate
{
  let interfaceCopy = InterfaceCopyBindings()
  private let store: TrafficAlertsStore
  private let master = PaxToggle(label: "", showsLabel: false)
  private let short = TrafficAlertRuleControls()
  private let medium = TrafficAlertRuleControls()
  private let day = TrafficAlertRuleControls()
  private let duration = PaxRoutePicker(style: .toolbar)
  private var group: PaxPreferenceGroup?
  private var draft: TrafficAlertSettings?
  private var dirty = false
  private var feedback = ""
  private lazy var save = PaxRouteButton(
    "", style: .primary, target: self, action: #selector(commit))
  private lazy var retry = PaxRouteButton(
    "", style: .quiet, target: self, action: #selector(reload))
  private lazy var systemSettings = PaxRouteButton(
    "", style: .quiet, target: self, action: #selector(openNotifications))

  init(store: TrafficAlertsStore) {
    self.store = store
    super.init(nibName: nil, bundle: nil)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    master.target = self
    master.action = #selector(changed)
    master.setAccessibilityIdentifier("traffic-alerts-enabled")
    interfaceCopy.bind(master, key: "alerts.enabled") { $0.apply(label: $1) }
    interfaceCopy.picker(duration, keys: ["alerts.hour", "alerts.two_hours"])
    duration.target = self
    duration.action = #selector(windowChanged)
    duration.widthAnchor.constraint(equalToConstant: PaxTheme.Settings.pickerWidth).isActive = true
    duration.setAccessibilityIdentifier("traffic-alerts-window")
    interfaceCopy.bind(duration, key: "alerts.medium") { $0.setAccessibilityLabel($1) }
    for (controls, key) in [(short, "short"), (medium, "medium"), (day, "day")] {
      controls.onChange = { [weak self] in self?.changed() }
      controls.configure(key: key, copy: interfaceCopy, delegate: self)
    }
    interfaceCopy.button(save, key: "alerts.save")
    interfaceCopy.button(retry, key: "settings.reload")
    interfaceCopy.button(systemSettings, key: "alerts.system_settings")
    save.setAccessibilityIdentifier("traffic-alerts-save")
    retry.setAccessibilityIdentifier("traffic-alerts-reload")
    let actions = NSStackView(views: [systemSettings, NSView(), save])
    actions.spacing = PaxTheme.Spacing.tight
    let sustained = NSStackView(views: [duration, medium])
    sustained.spacing = PaxTheme.Spacing.tight
    sustained.widthAnchor.constraint(
      equalTo: medium.widthAnchor,
      constant: PaxTheme.Settings.pickerWidth + PaxTheme.Spacing.tight
    ).isActive = true
    let group = PaxPreferenceGroup(
      title: "", symbol: "bell",
      rows: [
        interfaceCopy.preference(title: "alerts.enabled", detail: "alerts.scope", control: master),
        interfaceCopy.preference(
          title: "alerts.short", detail: "alerts.short_detail", control: short),
        interfaceCopy.preference(
          title: "alerts.medium", detail: "alerts.medium_detail", control: sustained),
        interfaceCopy.preference(title: "alerts.day", detail: "alerts.day_detail", control: day),
      ], style: .compact, headingAccessory: retry, footer: actions)
    self.group = group
    view = interfaceCopy.group(group, key: "alerts.title")
    view.setAccessibilityIdentifier("traffic-alerts-settings")
    interfaceCopy.onChange = { [weak self] in self?.renderStatus() }
    observe()
  }
  private func observe() {
    withObservationTracking {
      update()
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observe() }
    }
  }
  private func update() {
    if let value = store.state?.settings, !dirty, draft != value { render(value) }
    renderStatus()
    let available = store.connected && store.supported && store.state != nil && !store.busy
    master.isEnabled = available
    duration.isEnabled = available
    for controls in [short, medium, day] { controls.setEnabled(available) }
    save.isEnabled = available && dirty
    retry.isEnabled = store.connected && store.supported && !store.busy
    retry.isHidden = store.error == nil && store.state?.monitorState != "failed"
    systemSettings.isHidden = !store.notificationDenied
  }

  private func render(_ value: TrafficAlertSettings) {
    draft = value
    master.isChecked = value.enabled
    short.apply(value.short)
    medium.apply(value.medium)
    day.apply(value.day)
    duration.selectItem(at: value.mediumSeconds == 7_200 ? 1 : 0)
  }
  private func renderStatus() {
    let c = interfaceCopy.context
    var parts: [String] = []
    if !store.connected || !store.supported {
      parts.append(c.text("settings.backend_unavailable"))
    } else if store.busy {
      parts.append(c.text("settings.saving"))
    } else if let state = store.state {
      parts.append(c.text("alerts.state." + state.monitorState))
    }
    if let state = store.state, !state.errorCode.isEmpty {
      parts.append(c.text("alerts.backend_error"))
    }
    if store.notificationDenied { parts.append(c.text("alerts.denied")) }
    if let error = store.notificationError {
      parts.append(c.text("alerts.delivery_failed", ["error": error]))
    }
    if let error = store.error {
      parts.append(
        c.text(
          "settings.save_failed", ["detail": ProductErrorPresentation.display(error, context: c)]))
    }
    if !feedback.isEmpty { parts.append(c.text(feedback)) }
    parts.append(c.text("alerts.note"))
    group?.apply(summary: parts.joined(separator: " · "))
  }
  @objc private func changed() {
    dirty = true
    feedback = "settings.unsaved"
    update()
  }
  func controlTextDidChange(_ notification: Notification) { changed() }
  @objc private func windowChanged() {
    guard var value = draft, let rule = medium.value else {
      duration.selectItem(at: draft?.mediumSeconds == 7_200 ? 1 : 0)
      feedback = "alerts.invalid"
      renderStatus()
      return
    }
    value.medium = rule
    value.mediumSeconds = duration.indexOfSelectedItem == 1 ? 7_200 : 3_600
    draft = value
    medium.apply(value.medium)
    changed()
  }
  @objc private func reload() {
    Task { [weak self] in
      guard let self else { return }
      await store.refresh()
      if store.error == nil, let settings = store.state?.settings {
        dirty = false
        feedback = ""
        render(settings)
      }
      update()
    }
  }
  @objc private func commit() {
    guard var value = draft, let shortValue = short.value, let mediumValue = medium.value,
      let dayValue = day.value
    else {
      feedback = "alerts.invalid"
      renderStatus()
      return
    }
    value.enabled = master.isChecked
    value.short = shortValue
    value.medium = mediumValue
    value.day = dayValue
    Task { [weak self] in
      guard let self else { return }
      if await store.save(value), let saved = store.state?.settings {
        dirty = false
        feedback = "alerts.saved"
        render(saved)
      }
      update()
    }
  }
  @objc private func openNotifications() {
    if let url = URL(string: "x-apple.systempreferences:com.apple.Notifications-Settings.extension")
    {
      NSWorkspace.shared.open(url)
    }
  }
}

@MainActor private final class TrafficAlertRuleControls: NSStackView {
  var onChange: (() -> Void)?
  private let enabled = PaxToggle(label: "", showsLabel: false)
  private let upload = PaxRouteField("")
  private let download = PaxRouteField("")
  private let uploadUnit = PaxRoutePicker(style: .toolbar)
  private let downloadUnit = PaxRoutePicker(style: .toolbar)
  init() {
    super.init(frame: .zero)
    spacing = PaxTheme.Spacing.tight
    alignment = .centerY
    for unit in [uploadUnit, downloadUnit] {
      unit.addItems(withTitles: ["MB", "GB"])
      unit.target = self
      unit.action = #selector(changed)
      unit.widthAnchor.constraint(equalToConstant: PaxTheme.Settings.numberWidth).isActive = true
    }
    for field in [upload, download] {
      field.widthAnchor.constraint(equalToConstant: PaxTheme.Settings.numberWidth).isActive = true
    }
    enabled.target = self
    enabled.action = #selector(changed)
    let arrows = [PaxText.label("↑"), PaxText.label("↓")]
    for view in [enabled, arrows[0], upload, uploadUnit, arrows[1], download, downloadUnit] {
      addArrangedSubview(view)
    }
    widthAnchor.constraint(
      equalToConstant:
        enabled.intrinsicContentSize.width + 4 * PaxTheme.Settings.numberWidth
        + arrows.reduce(CGFloat.zero) { $0 + $1.intrinsicContentSize.width }
        + 6 * PaxTheme.Spacing.tight
    ).isActive = true
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  func configure(key: String, copy: InterfaceCopyBindings, delegate: NSTextFieldDelegate) {
    enabled.setAccessibilityIdentifier("traffic-alerts-" + key)
    copy.bind(enabled, key: "alerts." + key) { $0.apply(label: $1) }
    for (field, unit, direction) in [
      (upload, uploadUnit, "upload"), (download, downloadUnit, "download"),
    ] {
      field.delegate = delegate
      field.setAccessibilityIdentifier("traffic-alerts-" + key + "-" + direction)
      copy.bind(field, key: "alerts." + direction) { $0.setAccessibilityLabel($1) }
      copy.bind(unit, key: "alerts.unit") { $0.setAccessibilityLabel($1) }
    }
  }
  func apply(_ value: TrafficAlertRule) {
    enabled.isChecked = value.enabled
    for (bytes, field, unit) in [
      (value.upload, upload, uploadUnit), (value.download, download, downloadUnit),
    ] {
      let gigabytes = bytes >= 1_000_000_000 && bytes % 1_000_000_000 == 0
      unit.selectItem(at: gigabytes ? 1 : 0)
      field.stringValue = (Double(bytes) / (gigabytes ? 1_000_000_000 : 1_000_000)).formatted(
        .number.grouping(.never).precision(.fractionLength(0...6)))
    }
  }
  var value: TrafficAlertRule? {
    func bytes(_ field: NSTextField, _ unit: NSPopUpButton) -> UInt64? {
      guard let number = Decimal(string: field.stringValue, locale: .current) else { return nil }
      var amount = number * (unit.indexOfSelectedItem == 1 ? 1_000_000_000 : 1_000_000)
      var rounded = Decimal()
      NSDecimalRound(&rounded, &amount, 0, .plain)
      guard amount == rounded, amount >= 1, amount <= Decimal(UInt64.max) else { return nil }
      return NSDecimalNumber(decimal: amount).uint64Value
    }
    guard let up = bytes(upload, uploadUnit), let down = bytes(download, downloadUnit) else {
      return nil
    }
    return .init(enabled: enabled.isChecked, upload: up, download: down)
  }
  func setEnabled(_ value: Bool) {
    enabled.isEnabled = value
    for control in [upload, download] { control.isEnabled = value }
    for control in [uploadUnit, downloadUnit] { control.isEnabled = value }
  }
  @objc private func changed() { onChange?() }
}
