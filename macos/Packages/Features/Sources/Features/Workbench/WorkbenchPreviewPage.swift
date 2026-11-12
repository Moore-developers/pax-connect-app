import AppKit
import Observation
import PaxUI

/// 恢复产品侧栏后的浏览预览：只展示同一个演示会话，不伪装完整 U4 功能。
@MainActor
final class WorkbenchPreviewPage: NSViewController {
  enum Section: String {
    case routing = "分流"
    case about = "关于"
  }
  private let store: WorkbenchDemoStore
  private let section: Section
  private let content = NSStackView()
  private var table: PaxDataTable?
  init(store: WorkbenchDemoStore, section: Section) {
    self.store = store
    self.section = section
    super.init(nibName: nil, bundle: nil)
  }
  @available(*, unavailable)
  required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    let root = WorkbenchSurface()
    let scroll = NSScrollView()
    scroll.drawsBackground = false
    scroll.hasVerticalScroller = true
    let document = WorkbenchDocument()
    document.translatesAutoresizingMaskIntoConstraints = false
    scroll.documentView = document
    content.orientation = .vertical
    content.alignment = .leading
    content.spacing = PaxTheme.Spacing.cardGap
    content.translatesAutoresizingMaskIntoConstraints = false
    document.addSubview(content)
    scroll.translatesAutoresizingMaskIntoConstraints = false
    root.addSubview(scroll)
    NSLayoutConstraint.activate([
      scroll.leadingAnchor.constraint(equalTo: root.leadingAnchor),
      scroll.trailingAnchor.constraint(equalTo: root.trailingAnchor),
      scroll.topAnchor.constraint(equalTo: root.safeAreaLayoutGuide.topAnchor),
      scroll.bottomAnchor.constraint(equalTo: root.bottomAnchor),
      document.widthAnchor.constraint(equalTo: scroll.contentView.widthAnchor),
      content.leadingAnchor.constraint(
        equalTo: document.leadingAnchor, constant: PaxTheme.Spacing.page),
      content.trailingAnchor.constraint(
        equalTo: document.trailingAnchor, constant: -PaxTheme.Spacing.page),
      content.topAnchor.constraint(equalTo: document.topAnchor, constant: PaxTheme.Spacing.pageTop),
      content.bottomAnchor.constraint(
        equalTo: document.bottomAnchor, constant: -PaxTheme.Spacing.page),
    ])
    view = root
    add(PaxText.label(section.rawValue, font: PaxTheme.Typography.title()))
    switch section {
    case .routing:
      let table = PaxDataTable(columns: [
        .init("对象", width: PaxTheme.Workbench.identityColumn),
        .init("最终去向", width: PaxTheme.Workbench.directionColumn),
        .init("下载", width: PaxTheme.Workbench.downloadColumn, numeric: true),
        .init("上传", width: PaxTheme.Workbench.uploadColumn, numeric: true),
      ])
      self.table = table
      addCard("当前维度的流量去向", table, height: PaxTheme.Workbench.detailsTable)
    case .about:
      addCard(
        "Pax Connect",
        PaxNotice(
          title: "原生连接工作台", detail: "macOS 网络连接工具。"))
    }
    observe()
  }
  private func add(_ view: NSView) {
    content.addArrangedSubview(view)
    view.widthAnchor.constraint(equalTo: content.widthAnchor).isActive = true
  }
  private func addCard(_ title: String, _ body: NSView, height: CGFloat? = nil) {
    let stack = NSStackView(views: [PaxSectionHeader(title), body])
    stack.orientation = .vertical
    stack.alignment = .leading
    stack.spacing = PaxTheme.Spacing.regular
    for item in stack.arrangedSubviews {
      item.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
    }
    if let height { body.heightAnchor.constraint(equalToConstant: height).isActive = true }
    let card = PaxCard()
    card.contentView = stack
    add(card)
  }
  private func observe() {
    withObservationTracking {
      if section == .routing {
        table?.apply(rows: store.detailRows)
      }
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observe() }
    }
  }
}
