import AppKit
import PaxCore
import PaxUI

/// Single-selection grid shared by the application and process rule tabs.
@MainActor
final class RoutingRuleCandidateGrid: NSView, NSCollectionViewDataSource,
  NSCollectionViewDelegateFlowLayout
{
  struct Candidate: Equatable {
    let id: String
    let name: String
    var path: String? = nil
    let bytes: UInt64

    var searchText: String { name + " " + id }
  }

  private var columns = 2
  private static let maximumVisibleRows = 4
  private let collection = NSCollectionView()
  private let scroll = NSScrollView()
  private let empty = PaxText.label("", secondary: true)
  private let flow = NSCollectionViewFlowLayout()
  private let icons: WorkbenchObjectIcons
  private var items: [Candidate] = []
  private var height: NSLayoutConstraint?
  private var layoutWidth: CGFloat = 0
  var onSelect: ((Candidate) -> Void)?

  init(icons: WorkbenchObjectIcons) {
    self.icons = icons
    super.init(frame: .zero)
    translatesAutoresizingMaskIntoConstraints = false
    flow.minimumLineSpacing = 0
    flow.minimumInteritemSpacing = PaxTheme.Spacing.regular
    collection.collectionViewLayout = flow
    collection.register(
      RoutingRuleCandidateItem.self, forItemWithIdentifier: RoutingRuleCandidateItem.reuseID)
    collection.isSelectable = true
    collection.autoresizingMask = [.width]
    collection.backgroundColors = [.clear]
    collection.delegate = self
    collection.dataSource = self
    scroll.documentView = collection
    scroll.hasVerticalScroller = true
    scroll.autohidesScrollers = true
    scroll.drawsBackground = false
    for child in [scroll, empty] as [NSView] {
      child.translatesAutoresizingMaskIntoConstraints = false
      addSubview(child)
    }
    let height = heightAnchor.constraint(equalToConstant: PaxTheme.Table.memberRow)
    self.height = height
    NSLayoutConstraint.activate([
      height,
      scroll.leadingAnchor.constraint(equalTo: leadingAnchor),
      scroll.trailingAnchor.constraint(equalTo: trailingAnchor),
      scroll.topAnchor.constraint(equalTo: topAnchor),
      scroll.bottomAnchor.constraint(equalTo: bottomAnchor),
      empty.leadingAnchor.constraint(equalTo: leadingAnchor),
      empty.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor),
      empty.centerYAnchor.constraint(equalTo: centerYAnchor),
    ])
  }

  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  func apply(_ values: [Candidate], selected: String, emptyMessage: String, isProcess: Bool) {
    collection.setAccessibilityIdentifier(
      isProcess ? "routing-process-candidates" : "routing-application-candidates")
    collection.setAccessibilityLabel(isProcess ? "选择进程" : "选择应用")
    let requestedColumns = isProcess ? 3 : 2
    if items != values || columns != requestedColumns {
      columns = requestedColumns
      flow.invalidateLayout()
      items = values
      collection.reloadData()
      let rows = min(
        Self.maximumVisibleRows, max(1, (values.count + columns - 1) / columns))
      height?.constant =
        CGFloat(rows) * PaxTheme.Table.memberRow
    }
    empty.stringValue = emptyMessage
    empty.isHidden = !values.isEmpty
    scroll.isHidden = values.isEmpty
    let selection =
      items.firstIndex { $0.id == selected }.map {
        Set([IndexPath(item: $0, section: 0)])
      } ?? []
    if collection.selectionIndexPaths != selection {
      collection.selectionIndexPaths = selection
    }
  }

  override func layout() {
    super.layout()
    let width = scroll.contentSize.width
    guard width != layoutWidth else { return }
    layoutWidth = width
    flow.invalidateLayout()
  }

  func collectionView(_ collectionView: NSCollectionView, numberOfItemsInSection section: Int)
    -> Int
  {
    items.count
  }
  func collectionView(
    _ collectionView: NSCollectionView, itemForRepresentedObjectAt path: IndexPath
  )
    -> NSCollectionViewItem
  {
    let item = collectionView.makeItem(withIdentifier: RoutingRuleCandidateItem.reuseID, for: path)
    let candidate = items[path.item]
    let icon =
      candidate.path == nil
      ? icons.image(kind: .application, id: candidate.id, name: candidate.name)
      : nil
    (item as? RoutingRuleCandidateItem)?.apply(candidate, icon: icon)
    return item
  }
  func collectionView(
    _ collectionView: NSCollectionView, layout collectionViewLayout: NSCollectionViewLayout,
    sizeForItemAt indexPath: IndexPath
  ) -> NSSize {
    let gaps = CGFloat(columns - 1) * flow.minimumInteritemSpacing
    let width = floor((scroll.contentSize.width - gaps) / CGFloat(columns))
    return NSSize(width: max(1, width), height: PaxTheme.Table.memberRow)
  }
  func collectionView(_ collectionView: NSCollectionView, didSelectItemsAt paths: Set<IndexPath>) {
    guard let path = paths.first, items.indices.contains(path.item) else { return }
    onSelect?(items[path.item])
  }
}

@MainActor
private final class RoutingRuleCandidateItem: NSCollectionViewItem {
  static let reuseID = NSUserInterfaceItemIdentifier("routing-rule-candidate")
  private let background = NSBox()
  private let icon = NSImageView()
  private let name = PaxText.tableLabel("", identity: true)
  private let traffic = PaxText.tableLabel("", secondary: true, numeric: true, alignment: .right)
  private let check = NSImageView()
  private let separator = NSBox()

  override func loadView() {
    background.boxType = .custom
    background.cornerRadius = PaxTheme.Table.selectionRadius
    background.borderWidth = PaxTheme.Layout.cardBorder
    background.contentViewMargins = .zero
    separator.boxType = .separator
    traffic.setContentHuggingPriority(.required, for: .horizontal)
    traffic.setContentCompressionResistancePriority(.required, for: .horizontal)
    check.image = PaxIcon.image("checkmark", size: PaxTheme.Control.unit)
    check.contentTintColor = PaxTheme.Color.accent
    let row = NSStackView(views: [icon, name, traffic, check])
    row.spacing = PaxTheme.Spacing.regular
    let inset = PaxTheme.Table.inset
    if let content = background.contentView {
      for child in [row, separator] {
        child.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(child)
      }
      NSLayoutConstraint.activate([
        icon.widthAnchor.constraint(equalToConstant: PaxTheme.Control.height),
        icon.heightAnchor.constraint(equalTo: icon.widthAnchor),
        check.widthAnchor.constraint(equalToConstant: PaxTheme.Control.unit),
        row.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: inset),
        row.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -inset),
        row.centerYAnchor.constraint(equalTo: content.centerYAnchor),
        separator.leadingAnchor.constraint(equalTo: row.leadingAnchor),
        separator.trailingAnchor.constraint(equalTo: row.trailingAnchor),
        separator.bottomAnchor.constraint(equalTo: content.bottomAnchor),
      ])
    }
    view = background
    updateSelection()
  }

  override var isSelected: Bool {
    didSet { updateSelection() }
  }

  func apply(_ candidate: RoutingRuleCandidateGrid.Candidate, icon image: NSImage?) {
    loadViewIfNeeded()
    icon.image = image
    icon.imageScaling = .scaleProportionallyUpOrDown
    icon.isHidden = candidate.path != nil
    name.stringValue = candidate.name
    traffic.stringValue = TrafficFormatting.bytes(candidate.bytes)
    view.toolTip =
      (candidate.path ?? candidate.name + "\n" + candidate.id) + "\n" + traffic.stringValue
    view.setAccessibilityLabel(candidate.name)
    view.setAccessibilityHelp(candidate.path ?? candidate.id)
  }

  private func updateSelection() {
    background.fillColor = isSelected ? PaxTheme.Table.selected : .clear
    background.borderColor = isSelected ? PaxTheme.Color.accent : .clear
    separator.isHidden = isSelected
    check.alphaValue = isSelected ? 1 : 0
  }
}
