import AppKit
import Observation
import PaxUI

/// 复用系统设置的分组卡片，版本与许可说明始终可见。
@MainActor
final class AboutViewController: NSViewController {
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
    let identity = NSStackView(views: [
      PaxText.label("Pax Connect", font: PaxTheme.Typography.title()),
      PaxText.label(store.applicationVersion, font: PaxTheme.Typography.time, secondary: true),
    ])
    identity.orientation = .vertical
    identity.alignment = .leading
    identity.spacing = PaxTheme.Spacing.xs
    let intro = NSStackView(views: [icon, identity, NSView()])
    intro.spacing = PaxTheme.Spacing.regular
    add(intro)
    add(
      PaxPreferenceGroup(
        title: "版本信息", symbol: "info.circle",
        rows: [
          row("内置 sing-box", detail: "网络内核", value: label(store.kernel)),
          row("随包 daemon", detail: "应用内置的后台服务", value: label(store.bundledDaemon)),
          row("已连接 daemon", detail: "当前连接的后台服务", value: daemon),
          row("操作系统", detail: "当前系统版本", value: label(store.system)),
          row("芯片架构", detail: "应用构建架构", value: label(store.architecture)),
        ]))
    add(
      PaxPreferenceGroup(
        title: "开源许可", symbol: "doc.text",
        rows: AboutStore.licenses.map { entry in
          row(entry.name, detail: entry.detail, value: label(entry.license))
        }))
    observe()
  }
  private func add(_ item: NSView) {
    content.addArrangedSubview(item)
    item.widthAnchor.constraint(equalTo: content.widthAnchor).isActive = true
  }
  private func label(_ value: String) -> NSTextField {
    PaxText.label(value, font: PaxTheme.Typography.time)
  }
  private func row(_ title: String, detail: String, value: NSTextField) -> NSView {
    PaxPreferenceRow(title: title, detail: detail, control: value)
  }
  private func observe() {
    withObservationTracking {
      daemon.stringValue = store.runningDaemon
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observe() }
    }
  }
}
