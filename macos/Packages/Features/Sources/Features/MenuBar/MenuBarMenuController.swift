import AppKit
import PaxCore
import PaxUI

/// The same native menu is used for every status-item activation.
@MainActor
public final class MenuBarMenuController: NSObject, NSMenuDelegate {
  public enum Destination {
    case dashboard, routes, importRoutes, traffic, logs, settings, diagnostics, toggleProxy
  }
  public let menu = NSMenu(title: "Pax Connect")
  public static var loadingImage: NSImage { PaxMenuBarIcon.image(isActive: false) }
  public var onStatusChange: ((NSImage, String) -> Void)? { didSet { update() } }
  public var isTerminating = false { didSet { update() } }
  let session: MenuBarSession
  private let navigate: (Destination) -> Void
  private let quit: () -> Void
  private let selectedItem = NSMenuItem()
  private let emptyRoutesItem = NSMenuItem(title: "暂无可用线路", action: nil, keyEquivalent: "")
  private var routeItems: [NSMenuItem] = []
  private let statusItem = NSMenuItem()
  private let toggleItem = NSMenuItem()
  private let feedbackItem = NSMenuItem()
  private let problemItem = NSMenuItem()
  private let quitItem = NSMenuItem()

  public init(
    routes: RoutesStore, connection: ConnectionStore,
    onNavigate: @escaping (Destination) -> Void, onQuit: @escaping () -> Void
  ) {
    session = MenuBarSession(routes: routes, connection: connection)
    navigate = onNavigate
    quit = onQuit
    super.init()
    configure()
  }
  public init(
    demo: WorkbenchDemoStore,
    onNavigate: @escaping (Destination) -> Void, onQuit: @escaping () -> Void
  ) {
    session = MenuBarSession(routes: demo.catalog, connection: demo.connection, demo: demo)
    navigate = onNavigate
    quit = onQuit
    super.init()
    configure()
  }
  private func configure() {
    menu.autoenablesItems = false
    menu.delegate = self
    session.onChange = { [weak self] in self?.update() }
    build()
    update()
  }
  public func menuWillOpen(_ menu: NSMenu) {
    session.open()
    build()
    update()
  }
  public func menuDidClose(_ menu: NSMenu) { session.close() }
  private func build() {
    menu.removeAllItems()
    routeItems = []
    statusItem.isEnabled = false
    menu.addItem(statusItem)
    configure(toggleItem, title: "", action: #selector(toggle))
    menu.addItem(toggleItem)
    feedbackItem.isEnabled = false
    menu.addItem(feedbackItem)
    menu.addItem(.separator())
    selectedItem.target = self
    selectedItem.action = #selector(selectRoute(_:))
    menu.addItem(selectedItem)
    emptyRoutesItem.isEnabled = false
    menu.addItem(emptyRoutesItem)
    for row in session.recommendationRows { addRoute(row) }
    menu.addItem(.separator())
    add("Dashboard", .dashboard, symbol: "square.grid.2x2")
    add("导入线路", .importRoutes, symbol: "square.and.arrow.down")
    add("全部线路", .routes, symbol: "point.3.connected.trianglepath.dotted")
    add("流量分析", .traffic, symbol: "chart.xyaxis.line")
    add("日志记录", .logs, symbol: "list.bullet.rectangle")
    add("系统设置", .settings, symbol: "gearshape")
    configure(problemItem, title: "查看连接问题…", action: #selector(showProblems))
    problemItem.image = icon("exclamationmark.triangle")
    menu.addItem(problemItem)
    menu.addItem(.separator())
    configure(quitItem, title: "", action: #selector(terminate))
    quitItem.image = icon("power")
    menu.addItem(quitItem)
    if #available(macOS 27.0, *) {
      for item in menu.items { item.preferredImageVisibility = .visible }
    }
  }
  private func configure(_ item: NSMenuItem, title: String, action: Selector) {
    item.title = title
    item.target = self
    item.action = action
  }
  private func add(_ title: String, _ destination: Destination, symbol: String) {
    let item = NSMenuItem(title: title, action: #selector(openPage(_:)), keyEquivalent: "")
    item.target = self
    item.representedObject = destination
    item.image = icon(symbol)
    menu.addItem(item)
  }
  private func icon(_ symbol: String) -> NSImage? {
    let image = NSImage(systemSymbolName: symbol, accessibilityDescription: nil)
    image?.isTemplate = true
    return image
  }
  private func addRoute(_ row: MenuBarSession.Row) {
    let item = NSMenuItem(title: "", action: #selector(selectRoute(_:)), keyEquivalent: "")
    item.target = self
    item.representedObject = row.id
    item.identifier = NSUserInterfaceItemIdentifier("menu-route-" + row.id)
    item.image = icon("network")
    routeItems.append(item)
    menu.addItem(item)
  }
  private func update() {
    onStatusChange?(session.statusImage, "Pax Connect · " + session.status)
    statusItem.title = "Pax Connect · " + (isTerminating ? "正在退出…" : session.status)
    statusItem.image = session.statusImage
    toggleItem.image = icon(session.connection.proxyEnabled ? "pause.circle" : "play.circle")
    toggleItem.title = session.connection.proxyEnabled ? "关闭代理" : "开启代理"
    toggleItem.isEnabled = session.canToggle && !isTerminating
    feedbackItem.title = session.feedback ?? ""
    feedbackItem.isHidden = session.feedback == nil
    problemItem.isHidden = !session.hasProblem
    problemItem.isEnabled = !isTerminating
    quitItem.title = session.connection.proxyEnabled ? "关闭代理并退出" : "退出 Pax Connect"
    quitItem.isEnabled = !isTerminating
    for item in menu.items where item.action == #selector(openPage(_:)) {
      item.isEnabled = !isTerminating
    }
    emptyRoutesItem.isHidden = session.selectedRow != nil || !session.recommendationRows.isEmpty
    if let selected = session.selectedRow {
      selectedItem.isHidden = false
      updateRoute(selectedItem, row: selected)
    } else {
      selectedItem.isHidden = true
    }
    for (index, item) in routeItems.enumerated() where index < session.recommendationRows.count {
      updateRoute(item, row: session.recommendationRows[index])
    }
  }
  private func updateRoute(_ item: NSMenuItem, row: MenuBarSession.Row) {
    item.image = icon("network")
    let name =
      row.route.name.count > PaxTheme.MenuBar.maximumNameCharacters
      ? String(row.route.name.prefix(PaxTheme.MenuBar.maximumNameCharacters - 1)) + "…"
      : row.route.name
    item.title = name + "\t" + row.latency
    let paragraph = NSMutableParagraphStyle()
    paragraph.tabStops = [
      NSTextTab(textAlignment: .right, location: PaxTheme.MenuBar.latencyTabStop)
    ]
    let title = NSMutableAttributedString(
      string: item.title, attributes: [.paragraphStyle: paragraph])
    title.addAttribute(
      .font, value: PaxTheme.MenuBar.latencyFont,
      range: (item.title as NSString).range(of: row.latency, options: .backwards))
    item.attributedTitle = title
    item.state = row.checked ? .on : .off
    item.isEnabled = row.enabled && !isTerminating
    item.toolTip = row.route.name + "；" + row.detail
    item.setAccessibilityLabel(row.route.name + (row.checked ? "，当前线路，" : "，") + row.latency)
    item.representedObject = row.id
  }
  @objc private func selectRoute(_ sender: NSMenuItem) {
    guard !isTerminating, let id = sender.representedObject as? String else { return }
    session.select(id)
  }
  @objc private func openPage(_ sender: NSMenuItem) {
    guard !isTerminating, let destination = sender.representedObject as? Destination else { return }
    navigate(destination)
  }
  @objc private func toggle() {
    guard !isTerminating else { return }
    if let demo = session.demo { demo.perform(.toggleProxy) } else { navigate(.toggleProxy) }
  }
  @objc private func showProblems() { if !isTerminating { navigate(.diagnostics) } }
  @objc private func terminate() { if !isTerminating { quit() } }
}
