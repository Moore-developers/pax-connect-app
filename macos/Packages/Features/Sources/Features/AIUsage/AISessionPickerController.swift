import AppKit
import PaxCore
import PaxUI

/// Native directory browsing follows the page range using last native activity.
@MainActor final class AISessionPickerController: NSViewController {
  var onChoose: ((AISessionKey?) -> Void)?
  private let catalog: AISessionCatalogStore
  private var selected: AISessionKey?
  private let tabsHost = NSStackView()
  private let count = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private let search = NSSearchField()
  private let status = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private let more = PaxCompactButton("加载更多", target: nil, action: nil)
  private lazy var footer = AIUsageLayout.wrap(more)
  private var entries: [String: AISessionCatalogEntry] = [:]
  private var renderedItems: [AISessionCatalogEntry] = []
  private var emptyMessage = ""
  private var groupTitles: [String: String] = [:]
  private var renderedCLI = ""
  private var renderedDay: Date?
  private var renderedSelection: AISessionKey?
  private var tabCounts: [String: Int] = [:]
  private var tabCLI = ""
  private var clients: [String] {
    [""]
      + AIClient.allCases.map(\.code).filter {
        catalog.counts[$0, default: 0] > 0 || $0 == catalog.cli
      }
  }
  private let table = PaxDataTable(
    columns: [
      .init(
        "会话", width: PaxTheme.AISession.pickerWidth - PaxTheme.Spacing.regular * 2,
        inset: 0, sortable: false, contentLayout: .fill)
    ],
    metrics: .init(
      rowHeight: PaxTheme.AISession.pickerRowHeight, headerHeight: 0, rounded: false,
      rowSeparators: false, selectionInset: PaxTheme.Spacing.xs, background: PaxTheme.Color.card),
    selection: .single, scrollMode: .fixedHeader)

  init(catalog: AISessionCatalogStore, selected: AISessionKey?) {
    self.catalog = catalog
    self.selected = selected
    super.init(nibName: nil, bundle: nil)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  func prepare(selected: AISessionKey?) {
    self.selected = selected
    search.stringValue = ""
    let cli = selected?.cli ?? ""
    if catalog.cli != cli || !catalog.text.isEmpty {
      catalog.filter(cli: cli, text: "")
    } else {
      catalog.refresh()
    }
    refresh()
  }

  static func isMessagePreview(_ entry: AISessionCatalogEntry) -> Bool {
    ["claude.last-prompt", "claude.user-message"].contains(entry.titleSource)
  }

  static func title(_ entry: AISessionCatalogEntry, showIdentity: Bool = true) -> String {
    let fallback: String
    switch entry.titleStatus {
    case "source_unavailable": fallback = "会话名称暂不可读"
    case "source_missing": fallback = "源记录已移除"
    case "unsupported": fallback = "暂不支持的会话格式"
    case "indexing": fallback = "会话名称索引中"
    default: fallback = "客户端尚未命名"
    }
    let displayTitle =
      isMessagePreview(entry) && !entry.title.isEmpty
      ? "最近消息：" + entry.title : entry.title
    let identity = showIdentity ? " · " + String(entry.key.session.prefix(12)) : ""
    let client = showIdentity ? " · " + AIClientMark.shortName(entry.key.cli) : ""
    return (displayTitle.isEmpty ? fallback + identity : displayTitle) + client
  }

  override func loadView() {
    search.placeholderString = "搜索名称或会话 ID"
    search.setAccessibilityIdentifier("ai-session-search")
    search.setAccessibilityLabel("搜索会话")
    search.sendsSearchStringImmediately = true
    search.target = self
    search.action = #selector(searchChanged(_:))
    search.heightAnchor.constraint(equalToConstant: PaxTheme.Control.filterHeight).isActive = true
    tabsHost.orientation = .horizontal
    tabsHost.spacing = PaxTheme.Spacing.tight
    table.setTableIdentifier("ai-session-picker-list")
    table.cellProvider = { [weak self] id, _ in
      guard let self, let entry = entries[id] else { return nil }
      let cell = AISessionPickerCell()
      cell.apply(entry, showClient: catalog.cli.isEmpty, selected: entry.key == selected)
      return cell
    }
    table.cellUpdater = { [weak self] cell, id, _ in
      guard let cell = cell as? AISessionPickerCell, let entry = self?.entries[id] else {
        return false
      }
      cell.apply(
        entry, showClient: self?.catalog.cli.isEmpty == true, selected: entry.key == self?.selected)
      return true
    }
    table.canSelectRow = { [weak self] id in self?.entries[id] != nil }
    table.heightForRow = { [weak self] id in
      self?.groupTitles[id] == nil
        ? PaxTheme.AISession.pickerRowHeight : PaxTheme.Control.filterHeight
    }
    table.rowContent = { [weak self] id in
      guard let title = self?.groupTitles[id] else { return nil }
      let label = PaxText.label(title, font: PaxTheme.Typography.caption(), secondary: true)
      label.setAccessibilityRole(.staticText)
      return label
    }
    table.onSelect = { [weak self] id in if let id { self?.choose(id) } }
    table.onActivate = { [weak self] id in self?.choose(id) }
    more.target = self
    more.action = #selector(loadMore(_:))
    more.setAccessibilityIdentifier("ai-session-load-more")
    let body = AIUsageLayout.stack([
      AIUsageLayout.stack([AIUsageLayout.heading("选择会话"), NSView(), count], vertical: false),
      search, tabsHost, status, table, footer,
    ])
    body.spacing = PaxTheme.Spacing.regular
    let root = WorkbenchSurface()
    root.surfaceColor = PaxTheme.Color.card
    root.addSubview(body)
    body.translatesAutoresizingMaskIntoConstraints = false
    NSLayoutConstraint.activate([
      body.leadingAnchor.constraint(
        equalTo: root.leadingAnchor, constant: PaxTheme.Spacing.regular),
      body.trailingAnchor.constraint(
        equalTo: root.trailingAnchor, constant: -PaxTheme.Spacing.regular),
      body.topAnchor.constraint(equalTo: root.topAnchor, constant: PaxTheme.Spacing.regular),
      body.bottomAnchor.constraint(equalTo: root.bottomAnchor, constant: -PaxTheme.Spacing.regular),
    ])
    view = root
    preferredContentSize = NSSize(
      width: PaxTheme.AISession.pickerWidth, height: PaxTheme.AISession.pickerHeight)
    refresh()
  }

  func refresh() {
    guard isViewLoaded else { return }
    if tabCounts != catalog.counts || tabCLI != catalog.cli || tabsHost.arrangedSubviews.isEmpty {
      rebuildTabs()
    }
    let sourceMessage =
      catalog.sourceErrors.values.contains("unsupported")
      ? "部分会话格式暂不支持，保留可读记录" : "部分源记录暂不可读，保留已知会话"
    status.stringValue =
      catalog.error ?? catalog.collectionError
      ?? (!catalog.sourceErrors.isEmpty
        ? sourceMessage
        : !catalog.ready ? "正在索引本机会话…" : "")
    status.isHidden = status.stringValue.isEmpty
    count.stringValue = "所选日期 · \(catalog.total) 个"
    let message =
      catalog.items.isEmpty
      ? (catalog.loading ? "正在读取会话…" : "没有匹配的本机会话") : ""
    let today = Calendar.current.startOfDay(for: Date())
    if renderedItems != catalog.items || emptyMessage != message || renderedSelection != selected
      || renderedCLI != catalog.cli || renderedDay != today
    {
      let previous = entries
      entries = Dictionary(uniqueKeysWithValues: catalog.items.map { ($0.id, $0) })
      var changed = Set(catalog.items.filter { previous[$0.id] != $0 }.map(\.id))
      if renderedCLI != catalog.cli || renderedDay != today {
        changed.formUnion(catalog.items.map(\.id))
      }
      if renderedSelection != selected {
        changed.formUnion([renderedSelection?.id, selected?.id].compactMap { $0 })
      }
      let rows = groupedRows()
      if renderedDay != today { changed.formUnion(groupTitles.keys) }
      renderedItems = catalog.items
      renderedSelection = selected
      renderedCLI = catalog.cli
      renderedDay = today
      emptyMessage = message
      table.apply(
        rows: rows,
        emptyMessage: message, selectedID: selected?.id, reloading: changed,
        reusingVisibleCells: true)
    }
    footer.isHidden = !catalog.hasMore && catalog.error == nil
    more.isEnabled = !catalog.loading
    more.title = catalog.error == nil ? "加载更多（\(catalog.items.count)/\(catalog.total)）" : "重试"
  }
  private func rebuildTabs() {
    tabCounts = catalog.counts
    tabCLI = catalog.cli
    for item in tabsHost.arrangedSubviews {
      tabsHost.removeArrangedSubview(item)
      item.removeFromSuperview()
    }
    for (index, cli) in clients.enumerated() {
      let count = cli.isEmpty ? catalog.counts.values.reduce(0, +) : catalog.counts[cli, default: 0]
      let name = cli.isEmpty ? "全部" : AIClientMark.shortName(cli)
      let button = PaxCompactButton(
        name + " \(count)", style: cli == catalog.cli ? .selected : .text,
        target: self, action: #selector(filterChanged(_:)))
      button.tag = index
      button.setAccessibilityLabel(name + "（\(count)）")
      button.setAccessibilityValue(cli == catalog.cli ? "已选中" : "")
      tabsHost.addArrangedSubview(button)
    }
  }

  private func groupedRows() -> [PaxDataTable.Row] {
    var rows: [PaxDataTable.Row] = []
    groupTitles = [:]
    for entry in catalog.items {
      let date = entry.lastActivity
      let day =
        date.timeIntervalSince1970 > 0 ? TrafficFormatting.dateAndClock(date).date : "unknown"
      let id = "day:" + day
      if groupTitles[id] == nil {
        let title: String
        if day == "unknown" {
          title = "日期未记录"
        } else if Calendar.current.isDateInToday(date) {
          title = "今天"
        } else if Calendar.current.isDateInYesterday(date) {
          title = "昨天"
        } else {
          title = AIUsageFormatting.date(date)
        }
        groupTitles[id] = title
        rows.append(.init(id: id, values: [""]))
      }
      rows.append(.init(id: entry.id, values: [Self.title(entry, showIdentity: false)]))
    }
    return rows
  }

  @objc private func filterChanged(_ sender: NSButton) {
    let clients = self.clients
    guard clients.indices.contains(sender.tag) else { return }
    let cli = clients[sender.tag]
    if cli.isEmpty {
      selected = nil
      onChoose?(nil)
    }
    catalog.filter(cli: cli, text: search.stringValue)
    refresh()
  }
  private func choose(_ id: String) {
    guard let entry = catalog.items.first(where: { $0.id == id }) else { return }
    selected = entry.key
    onChoose?(entry.key)
  }
  @objc private func searchChanged(_ sender: NSSearchField) {
    catalog.filter(cli: catalog.cli, text: sender.stringValue)
  }
  @objc private func loadMore(_ sender: NSButton) {
    if catalog.error == nil { catalog.loadMore() } else { catalog.refresh() }
  }
}
