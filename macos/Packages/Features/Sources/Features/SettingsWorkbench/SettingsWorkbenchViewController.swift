import AppKit
import Observation
import PaxCore
import PaxUI

@MainActor
final class SettingsWorkbenchViewController: NSViewController {
  let store: SettingsWorkbenchStore
  private var logHistoryController: LogHistorySettingsController?
  private let privacyController: NetworkPrivacySettingsController?
  private let measurementController: MeasurementSettingsViewController?
  private lazy var tabs = PaxTabs(
    categories.map(\.title), symbols: categories.map(\.symbol))
  private var categories: [SettingsWorkbenchStore.Category] {
    SettingsWorkbenchStore.Category.allCases.filter {
      $0 != .measurement || measurementController != nil
    }
  }
  private let scroll = NSScrollView()
  private let content = NSStackView()
  private let banner = PaxText.label("", font: PaxTheme.Typography.caption(), wrapping: true)
  private let status = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private var pages: [SettingsWorkbenchStore.Category: NSView] = [:]
  private var rows: [SettingsWorkbenchStore.Field: PaxPreferenceRow] = [:]
  private var toggles: [SettingsWorkbenchStore.Field: PaxToggle] = [:]
  private var pickers: [SettingsWorkbenchStore.Field: PaxRoutePicker] = [:]
  private var advanced: NSView?
  private var serviceRows: [String: NSTextField] = [:]
  private var logRow: PaxPreferenceRow?
  private var serviceRow: PaxPreferenceRow?
  private weak var confirmationController: SettingsConfirmationController?
  private var shownCategory: SettingsWorkbenchStore.Category?
  private lazy var save = PaxRouteButton(
    "保存更改", style: .primary, target: self, action: #selector(saveChanges))
  private lazy var discard = PaxRouteButton(
    "撤销更改", target: self, action: #selector(discardChanges))
  private lazy var clear = PaxRouteButton(
    "清除日志…", target: self, action: #selector(clearLogs))
  private lazy var serviceButton = PaxRouteButton(
    "移除服务…", target: self, action: #selector(manageService))
  private lazy var disclosure = PaxRouteButton(
    "高级网络策略", symbol: "chevron.right", style: .quiet, target: self,
    action: #selector(toggleAdvanced))
  init(
    store: SettingsWorkbenchStore = SettingsWorkbenchStore(),
    measurement: MeasurementSettingsViewController? = nil, telemetry: TelemetryStore? = nil,
    privacyRoutes: RoutesStore? = nil
  ) {
    self.privacyController = privacyRoutes.map(NetworkPrivacySettingsController.init)
    self.store = store
    self.measurementController = measurement
    logHistoryController = telemetry.map(LogHistorySettingsController.init)
    super.init(nibName: nil, bundle: nil)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    view = WorkbenchSurface()
    view.setAccessibilityIdentifier("settings-page")
    let header = PaxPageHeader(
      title: "系统设置", accessory: PaxText.label("", font: PaxTheme.Typography.caption()))
    let footer = NSStackView(views: [status, NSView(), discard, save])
    footer.spacing = PaxTheme.Spacing.tight
    status.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    status.lineBreakMode = .byTruncatingTail
    status.setAccessibilityIdentifier("settings-status")
    save.setAccessibilityIdentifier("settings-save")
    discard.setAccessibilityIdentifier("settings-discard")
    let line = NSBox()
    line.boxType = .separator
    scroll.drawsBackground = false
    scroll.hasVerticalScroller = true
    scroll.autohidesScrollers = true
    scroll.automaticallyAdjustsContentInsets = false
    scroll.setAccessibilityIdentifier("settings-scroll")
    let document = WorkbenchDocument()
    document.translatesAutoresizingMaskIntoConstraints = false
    scroll.documentView = document
    content.orientation = .vertical
    content.alignment = .leading
    content.spacing = PaxTheme.Spacing.roomy
    content.translatesAutoresizingMaskIntoConstraints = false
    document.addSubview(content)
    banner.textColor = PaxTheme.Color.warning
    add(banner, to: content)
    for category in categories {
      let page = buildPage(category)
      pages[category] = page
      add(page, to: content)
    }
    for item in [header, tabs, scroll, line, footer] {
      item.translatesAutoresizingMaskIntoConstraints = false
      view.addSubview(item)
    }
    let inset = PaxTheme.Spacing.page
    NSLayoutConstraint.activate([
      header.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: inset),
      header.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -inset),
      header.topAnchor.constraint(
        equalTo: view.safeAreaLayoutGuide.topAnchor, constant: PaxTheme.Spacing.pageTop),
      tabs.leadingAnchor.constraint(equalTo: header.leadingAnchor),
      tabs.topAnchor.constraint(equalTo: header.bottomAnchor, constant: PaxTheme.Spacing.medium),
      tabs.trailingAnchor.constraint(lessThanOrEqualTo: header.trailingAnchor),
      scroll.leadingAnchor.constraint(equalTo: header.leadingAnchor),
      scroll.trailingAnchor.constraint(equalTo: header.trailingAnchor),
      scroll.topAnchor.constraint(equalTo: tabs.bottomAnchor, constant: PaxTheme.Spacing.roomy),
      scroll.bottomAnchor.constraint(equalTo: line.topAnchor, constant: -PaxTheme.Spacing.tight),
      line.leadingAnchor.constraint(equalTo: header.leadingAnchor),
      line.trailingAnchor.constraint(equalTo: header.trailingAnchor),
      line.bottomAnchor.constraint(equalTo: footer.topAnchor),
      footer.leadingAnchor.constraint(equalTo: header.leadingAnchor),
      footer.trailingAnchor.constraint(equalTo: header.trailingAnchor),
      footer.bottomAnchor.constraint(equalTo: view.bottomAnchor),
      footer.heightAnchor.constraint(equalToConstant: PaxTheme.Settings.footerHeight),
      document.widthAnchor.constraint(equalTo: scroll.contentView.widthAnchor),
      content.leadingAnchor.constraint(equalTo: document.leadingAnchor),
      content.trailingAnchor.constraint(equalTo: document.trailingAnchor),
      content.topAnchor.constraint(equalTo: document.topAnchor),
      content.bottomAnchor.constraint(
        equalTo: document.bottomAnchor, constant: -PaxTheme.Spacing.tight),
    ])
    tabs.onSelect = { [weak self] index in
      guard let category = SettingsWorkbenchStore.Category(rawValue: index) else { return }
      self?.store.perform(.select(category))
    }
    observe()
  }
  private func field(_ field: SettingsWorkbenchStore.Field) -> NSView {
    let control: NSView
    if let options = field.options {
      let picker = PaxRoutePicker(style: .toolbar)
      picker.addItems(withTitles: options)
      picker.target = self
      picker.action = #selector(pickerChanged(_:))
      picker.identifier = NSUserInterfaceItemIdentifier(field.rawValue)
      picker.setAccessibilityIdentifier("settings-" + field.rawValue)
      picker.setAccessibilityLabel(field.title)
      picker.widthAnchor.constraint(equalToConstant: PaxTheme.Settings.pickerWidth).isActive = true
      pickers[field] = picker
      control = picker
    } else {
      let toggle = PaxToggle(
        label: field.title, showsLabel: false, target: self, action: #selector(toggleChanged(_:)))
      toggle.identifier = NSUserInterfaceItemIdentifier(field.rawValue)
      toggle.setAccessibilityIdentifier("settings-" + field.rawValue)
      toggles[field] = toggle
      control = toggle
    }
    let row = PaxPreferenceRow(title: field.title, detail: field.detail, control: control)
    rows[field] = row
    return row
  }
  private func buildPage(_ category: SettingsWorkbenchStore.Category) -> NSView {
    let page = NSStackView()
    page.orientation = .vertical
    page.alignment = .leading
    page.spacing = PaxTheme.Spacing.roomy
    switch category {
    case .measurement:
      if let measurementController {
        addChild(measurementController)
        add(measurementController.view, to: page)
      }
    case .general:
      add(group("启动", symbol: "power", rows: [field(.login), field(.connect)]), to: page)
      add(
        group(
          "窗口与菜单栏", symbol: "macwindow",
          rows: [
            field(.dock), readOnly("关闭主窗口后", "保留菜单栏入口，代理继续运行。", value: "在后台运行"),
          ]), to: page)
      add(note("关闭主窗口会继续在后台运行；菜单退出或 ⌘Q 会关闭代理与后台并恢复网络。"), to: page)
    case .connection:
      if let privacyController {
        addChild(privacyController)
        add(privacyController.view, to: page)
      }
      add(
        group(
          "连接方式", symbol: "point.3.connected.trianglepath.dotted",
          rows: [field(.mode), field(.fastest)]), to: page)
      add(
        group(
          "网络恢复与访问", symbol: "shield.lefthalf.filled",
          rows: [
            field(.recovery), field(.guardTraffic), field(.lan),
          ]), to: page)
      let advanced = vertical([
        readOnly("IPv6", "未被接管的路径受限，连接状态由实际检查结果决定。", value: "随保护策略")
      ])
      self.advanced = advanced
      disclosure.setAccessibilityIdentifier("settings-advanced")
      let heading = NSStackView(views: [disclosure, NSView()])
      add(card(vertical([heading, advanced])), to: page)
    case .data:
      add(
        group("自动更新", symbol: "arrow.clockwise", rows: [field(.subscription), field(.rules)]),
        to: page)
      if let logHistoryController {
        addChild(logHistoryController)
        add(logHistoryController.view, to: page)
        break
      }
      let row = PaxPreferenceRow(
        title: "历史日志", detail: "保留期内的连接事件与诊断记录。", control: clear)
      logRow = row
      add(group("本地记录", symbol: "internaldrive", rows: [field(.retention), row]), to: page)
      add(note("清除日志不影响线路、分流规则或当前连接。"), to: page)
    case .service:
      let serviceState = valueLabel("运行中")
      serviceRows["state"] = serviceState
      let identity = NSStackView(views: [
        PaxRouteIconTile("server.rack"),
        vertical([
          PaxText.label("Pax Connect 后台服务", font: PaxTheme.Typography.label(weight: .medium)),
          note("负责连接、分流与网络恢复"),
        ]), NSView(), serviceState,
      ])
      identity.spacing = PaxTheme.Spacing.medium
      let permission = valueLabel("已允许")
      let connection = valueLabel("已连接")
      serviceRows["permission"] = permission
      serviceRows["connection"] = connection
      add(
        group(
          "服务状态", symbol: "server.rack",
          rows: [
            identity,
            PaxPreferenceRow(title: "系统授权", detail: "首次启用需在 macOS 系统设置中允许。", control: permission),
            PaxPreferenceRow(
              title: "与 App 的连接", detail: "关闭主窗口后，已开启的代理继续运行；退出 App 会关闭后台。", control: connection),
          ]), to: page)
      let row = PaxPreferenceRow(
        title: "后台服务", detail: "停止代理并恢复网络，然后移除服务。", control: serviceButton)
      serviceRow = row
      add(group("服务管理", symbol: "wrench", rows: [row]), to: page)
      add(note("移除服务保留线路和规则；再次连接前，需要重新启用并授权。"), to: page)
    }
    return page
  }
  private func vertical(_ views: [NSView]) -> NSStackView {
    let stack = NSStackView()
    stack.orientation = .vertical
    stack.alignment = .leading
    stack.spacing = PaxTheme.Spacing.xs
    for view in views { add(view, to: stack) }
    return stack
  }
  private func add(_ item: NSView, to stack: NSStackView) {
    stack.addArrangedSubview(item)
    item.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
  }
  private func group(_ title: String, symbol: String, rows: [NSView]) -> NSView {
    PaxPreferenceGroup(title: title, symbol: symbol, rows: rows)
  }

  private func card(_ body: NSView) -> NSView {
    let card = PaxCard(style: .routesSection)
    card.contentView = body
    body.bottomAnchor.constraint(equalTo: card.bottomAnchor, constant: -PaxTheme.Routes.sectionTop)
      .isActive = true
    return card
  }
  private func note(_ text: String) -> NSTextField {
    PaxText.label(text, font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  }
  private func valueLabel(_ value: String) -> NSTextField {
    PaxText.label(value, font: PaxTheme.Typography.caption(), secondary: true)
  }
  private func readOnly(_ title: String, _ detail: String, value: String) -> NSView {
    PaxPreferenceRow(title: title, detail: detail, control: valueLabel(value))
  }
  private func observe() {
    withObservationTracking {
      render()
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observe() }
    }
  }
  private func render() {
    tabs.selectedIndex = store.category.rawValue
    for (category, page) in pages { page.isHidden = category != store.category }
    for field in SettingsWorkbenchStore.Field.allCases {
      let change = store.changes.first { $0.field == field }
      rows[field]?.apply(
        detail: store.detail(field),
        currentValue: store.backendAvailable ? change.map { field.display($0.before) } : "尚未接入")
      toggles[field]?.isChecked = store.value(field) == 1
      toggles[field]?.isEnabled = store.canEdit(field)
      pickers[field]?.selectItem(at: store.value(field))
      pickers[field]?.isEnabled = store.canEdit(field)
    }
    banner.stringValue = logHistoryController != nil ? "未接入的设置保持禁用；日志与线路测量按各自状态保存。" : store.banner
    banner.isHidden = store.banner.isEmpty || store.category == .measurement
    let measuring = store.category == .measurement
    save.isHidden = measuring
    discard.isHidden = measuring
    status.stringValue = measuring ? "线路测量配置单独保存" : store.status
    status.toolTip = store.status
    status.textColor = store.failed ? PaxTheme.Color.error : PaxTheme.Color.textSecondary
    save.isEnabled = store.canSave
    save.title =
      store.operation == .save ? "正在保存…" : store.failedOperation == .save ? "重试保存" : "保存更改"
    discard.isEnabled = !store.busy && !store.changes.isEmpty
    advanced?.isHidden = !store.expanded
    disclosure.image = PaxRouteIcon.image(store.expanded ? "chevron.down" : "chevron.right")
    disclosure.setAccessibilityValue(store.expanded ? "已展开" : "已折叠")
    let values: (String, String, String, String, String)
    switch store.service {
    case .unavailable: values = ("尚未接入", "状态未知", "状态未知", "管理服务", "后台服务管理尚未接入，暂不可操作。")
    case .running: values = ("运行中", "已允许", "已连接", "移除服务…", "停止代理并恢复网络，然后移除服务。")
    case .disconnected: values = ("状态未知", "上次已允许", "连接断开", "重新连接", "重新建立与后台服务的连接。")
    case .pending: values = ("等待批准", "待允许", "尚未连接", "查看批准步骤", "在 macOS 的「登录项与扩展」中允许后台运行。")
    case .absent: values = ("未安装", "尚未授权", "尚未连接", "启用后台服务", "登记服务后，需前往系统设置批准。")
    }
    serviceRows["state"]?.stringValue = values.0
    serviceRows["state"]?.textColor =
      store.service == .running ? PaxTheme.Color.healthy : PaxTheme.Color.warning
    serviceRows["permission"]?.stringValue = values.1
    serviceRows["connection"]?.stringValue = values.2
    serviceButton.title = values.3
    serviceButton.contentTintColor =
      store.service == .running ? PaxTheme.Color.error : PaxTheme.Color.text
    serviceButton.isEnabled = store.backendAvailable && !store.busy
    serviceRow?.apply(detail: values.4, currentValue: nil)
    clear.isEnabled = !store.busy && store.service == .running
    logRow?.apply(
      detail: store.logsCleared ? "历史日志已清除，新事件会继续记录。" : "保留期内的连接事件与诊断记录。", currentValue: nil)
    if shownCategory != store.category {
      view.layoutSubtreeIfNeeded()
      scroll.contentView.scroll(to: .zero)
      scroll.reflectScrolledClipView(scroll.contentView)
      shownCategory = store.category
    }
    if let confirmation = store.confirmation, confirmationController == nil {
      let controller = SettingsConfirmationController(store: store, confirmation: confirmation)
      confirmationController = controller
      presentWorkbenchModal(controller)
    } else if store.confirmation == nil, let controller = confirmationController {
      dismiss(controller)
      confirmationController = nil
    }
  }
  @objc private func toggleChanged(_ sender: NSSwitch) {
    var ancestor = sender.superview
    while let view = ancestor {
      if let raw = view.identifier?.rawValue,
        let field = SettingsWorkbenchStore.Field(rawValue: raw)
      {
        store.perform(.edit(field, sender.state == .on ? 1 : 0))
        return
      }
      ancestor = view.superview
    }
  }
  @objc private func pickerChanged(_ sender: NSPopUpButton) {
    guard let raw = sender.identifier?.rawValue,
      let field = SettingsWorkbenchStore.Field(rawValue: raw)
    else { return }
    store.perform(.edit(field, sender.indexOfSelectedItem))
  }
  @objc private func saveChanges() { store.perform(.save) }
  @objc private func discardChanges() { store.perform(.discard) }
  @objc private func clearLogs() { store.perform(.clear) }
  @objc private func manageService() { store.perform(.manageService) }
  @objc private func toggleAdvanced() { store.perform(.toggleAdvanced) }
}
