import AppKit
import Observation
import PaxCore
import PaxUI

/// 复用系统设置的分组卡片，版本与许可说明始终可见。
@MainActor
final class AboutViewController: NSViewController, InterfaceLanguageConsumer {
  let interfaceCopy = InterfaceCopyBindings()
  private let version = PaxText.label("", font: PaxTheme.Typography.time, secondary: true)
  private let store: AboutStore
  private let content = NSStackView()
  private let scroll = NSScrollView()
  private let daemon = PaxText.label("", font: PaxTheme.Typography.time)
  init(store: AboutStore) {
    self.store = store
    super.init(nibName: nil, bundle: nil)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    view = WorkbenchSurface()
    view.setAccessibilityIdentifier("about-page")
    scroll.drawsBackground = false
    scroll.hasVerticalScroller = true
    scroll.autohidesScrollers = true
    let document = WorkbenchDocument()
    document.translatesAutoresizingMaskIntoConstraints = false
    scroll.documentView = document
    content.orientation = .vertical
    content.alignment = .leading
    content.spacing = PaxTheme.Spacing.roomy
    content.translatesAutoresizingMaskIntoConstraints = false
    document.addSubview(content)
    scroll.translatesAutoresizingMaskIntoConstraints = false
    view.addSubview(scroll)
    NSLayoutConstraint.activate([
      scroll.leadingAnchor.constraint(equalTo: view.leadingAnchor),
      scroll.trailingAnchor.constraint(equalTo: view.trailingAnchor),
      scroll.topAnchor.constraint(equalTo: view.safeAreaLayoutGuide.topAnchor),
      scroll.bottomAnchor.constraint(equalTo: view.bottomAnchor),
      document.widthAnchor.constraint(equalTo: scroll.contentView.widthAnchor),
      content.centerXAnchor.constraint(equalTo: document.centerXAnchor),
      content.widthAnchor.constraint(lessThanOrEqualToConstant: PaxTheme.About.contentWidth),
      content.leadingAnchor.constraint(
        greaterThanOrEqualTo: document.leadingAnchor, constant: PaxTheme.Spacing.page),
      content.topAnchor.constraint(equalTo: document.topAnchor, constant: PaxTheme.About.topInset),
      content.bottomAnchor.constraint(
        equalTo: document.bottomAnchor, constant: -PaxTheme.Spacing.roomy),
    ])
    let width = content.widthAnchor.constraint(equalToConstant: PaxTheme.About.contentWidth)
    width.priority = .defaultHigh
    width.isActive = true
    let icon = NSImageView(image: NSApp.applicationIconImage)
    icon.imageScaling = .scaleProportionallyUpOrDown
    icon.widthAnchor.constraint(equalToConstant: PaxTheme.About.iconSize).isActive = true
    icon.heightAnchor.constraint(equalTo: icon.widthAnchor).isActive = true
    interfaceCopy.onChange = { [weak self] in self?.renderIdentity() }
    let identity = NSStackView(views: [
      PaxText.label("Pax Connect", font: PaxTheme.Typography.title()),
      version,
    ])
    identity.orientation = .vertical
    identity.alignment = .leading
    identity.spacing = PaxTheme.Spacing.xs
    let intro = NSStackView(views: [icon, identity, NSView()])
    intro.spacing = PaxTheme.Spacing.regular
    add(intro)
    add(
      interfaceCopy.group(
        PaxPreferenceGroup(
          title: "", symbol: "info.circle",
          rows: [
            row("about.bundled_kernel", detail: "about.kernel", value: label(store.kernel)),
            row(
              "about.bundled_daemon", detail: "about.bundled_daemon_detail",
              value: label(store.bundledDaemon)),
            row("about.running_daemon", detail: "about.running_daemon_detail", value: daemon),
            row("about.system", detail: "about.system_detail", value: label(store.system)),
            row(
              "about.architecture", detail: "about.architecture_detail",
              value: label(store.architecture)),
          ], style: .compact), key: "about.versions"))
    add(
      interfaceCopy.group(
        PaxPreferenceGroup(
          title: "", symbol: "doc.text",
          rows: AboutStore.licenses.map { entry in
            licenseRow(
              entry.name, detail: entry.detail,
              value: entry.license.hasPrefix("about.")
                ? interfaceCopy.label(entry.license, font: PaxTheme.Typography.time)
                : label(entry.license))
          }, style: .compact), key: "about.licenses"))
    observe()
  }
  private func add(_ item: NSView) {
    content.addArrangedSubview(item)
    item.widthAnchor.constraint(equalTo: content.widthAnchor).isActive = true
  }
  private func label(_ value: String) -> NSTextField {
    value.isEmpty
      ? interfaceCopy.label("about.version_unavailable", font: PaxTheme.Typography.time)
      : PaxText.label(value, font: PaxTheme.Typography.time)
  }
  private func row(_ title: String, detail: String, value: NSTextField) -> NSView {
    interfaceCopy.preference(title: title, detail: detail, control: value)
  }
  private func licenseRow(_ title: String, detail: String, value: NSTextField) -> NSView {
    let row = PaxPreferenceRow(title: title, detail: "", control: value, style: .compact)
    return interfaceCopy.bind(row, key: detail) { row, detail in
      row.apply(title: title, detail: detail)
    }
  }
  private func renderIdentity() {
    version.stringValue = interfaceCopy.text(
      "about.version",
      [
        "version": store.version.isEmpty ? interfaceCopy.text("common.unknown") : store.version,
        "build": store.build.isEmpty ? interfaceCopy.text("common.unknown") : store.build,
      ])
    daemon.stringValue = store.runningDaemon(context: interfaceCopy.context)
  }
  private func observe() {
    withObservationTracking {
      renderIdentity()
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observe() }
    }
  }
}
