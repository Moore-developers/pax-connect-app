import AppKit
import PaxCore
import PaxUI

/// One settings page; each group keeps its existing save boundary.
@MainActor
final class SettingsWorkbenchViewController: NSViewController, InterfaceLanguageConsumer {
  let interfaceCopy = InterfaceCopyBindings()
  private let privacyController: NetworkPrivacySettingsController?
  private let logHistoryController: LogHistorySettingsController?
  private let measurementController: MeasurementSettingsViewController?
  private let mcpController: MCPAccessSettingsController?
  private let language: InterfaceLanguageStore?
  private let languageController: InterfaceLanguageSettingsController?
  private let content = NSStackView()
  private var expanded = false
  private lazy var disclosure = PaxRouteButton(
    "高级：线路测量", symbol: "chevron.right", style: .quiet, target: self,
    action: #selector(toggleMeasurement))

  init(
    measurement: MeasurementSettingsViewController? = nil, telemetry: TelemetryStore? = nil,
    privacyRoutes: RoutesStore? = nil, mcpAccess: MCPAccessActions? = nil,
    connection: ConnectionStore? = nil, language: InterfaceLanguageStore? = nil
  ) {
    self.language = language
    languageController = language.map(InterfaceLanguageSettingsController.init)
    mcpController = mcpAccess.flatMap { actions in
      connection.map { MCPAccessSettingsController(actions: actions, connection: $0) }
    }
    privacyController = privacyRoutes.map(NetworkPrivacySettingsController.init)
    logHistoryController = telemetry.map(LogHistorySettingsController.init)
    measurementController = measurement
    super.init(nibName: nil, bundle: nil)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  override func loadView() {
    view = WorkbenchSurface()
    view.setAccessibilityIdentifier("settings-page")
    let header = PaxPageHeader(
      title: self.interfaceCopy.text("navigation.settings"),
      accessory: PaxText.label("", font: PaxTheme.Typography.caption()))
    let scroll = NSScrollView()
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
    content.spacing = PaxTheme.Spacing.regular
    content.translatesAutoresizingMaskIntoConstraints = false
    document.addSubview(content)
    if let languageController { add(languageController) }
    interfaceCopy.bind(header, key: "navigation.settings") { $0.apply(title: $1) }
    interfaceCopy.button(disclosure, key: "settings.measurement.disclosure")
    interfaceCopy.onChange = { [weak self] in
      guard let self else { return }
      self.disclosure.setAccessibilityValue(
        self.interfaceCopy.text(self.expanded ? "settings.expanded" : "settings.collapsed"))
    }
    if let privacyController { add(privacyController) }
    if let logHistoryController { add(logHistoryController) }
    if let mcpController { add(mcpController) }
    if let measurementController {
      disclosure.setAccessibilityIdentifier("settings-measurement-disclosure")
      disclosure.setAccessibilityValue(interfaceCopy.text("settings.collapsed"))
      content.addArrangedSubview(disclosure)
      add(measurementController)
      measurementController.view.isHidden = true
    }
    if privacyController == nil && logHistoryController == nil && measurementController == nil {
      content.addArrangedSubview(
        PaxText.label(
          self.interfaceCopy.text("interface_detail.3fadfeeb66"),
          font: PaxTheme.Typography.caption(), secondary: true))
    }
    for item in [header, scroll] {
      item.translatesAutoresizingMaskIntoConstraints = false
      view.addSubview(item)
    }
    let inset = PaxTheme.Spacing.page
    NSLayoutConstraint.activate([
      header.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: inset),
      header.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -inset),
      header.topAnchor.constraint(
        equalTo: view.safeAreaLayoutGuide.topAnchor, constant: PaxTheme.Spacing.pageTop),
      scroll.leadingAnchor.constraint(equalTo: header.leadingAnchor),
      scroll.trailingAnchor.constraint(equalTo: header.trailingAnchor),
      scroll.topAnchor.constraint(equalTo: header.bottomAnchor, constant: PaxTheme.Spacing.roomy),
      scroll.bottomAnchor.constraint(equalTo: view.bottomAnchor),
      document.widthAnchor.constraint(equalTo: scroll.contentView.widthAnchor),
      content.leadingAnchor.constraint(equalTo: document.leadingAnchor),
      content.trailingAnchor.constraint(equalTo: document.trailingAnchor),
      content.topAnchor.constraint(equalTo: document.topAnchor),
      content.bottomAnchor.constraint(
        equalTo: document.bottomAnchor, constant: -PaxTheme.Spacing.roomy),
    ])
  }

  private func add(_ controller: NSViewController) {
    addChild(controller)
    content.addArrangedSubview(controller.view)
    controller.view.widthAnchor.constraint(equalTo: content.widthAnchor).isActive = true
  }

  @objc private func toggleMeasurement() {
    expanded.toggle()
    measurementController?.view.isHidden = !expanded
    disclosure.image = PaxRouteIcon.image(expanded ? "chevron.down" : "chevron.right")
    disclosure.setAccessibilityValue(
      interfaceCopy.text(expanded ? "settings.expanded" : "settings.collapsed"))
  }
}
