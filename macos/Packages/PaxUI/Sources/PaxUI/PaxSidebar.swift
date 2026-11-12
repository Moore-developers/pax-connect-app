import AppKit

/// 共享产品侧栏，只接收导航项和选择回调，不依赖页面或业务状态。
public final class PaxSidebar: NSView {
  public struct Item {
    /// 页面索引，与宿主的页序一致；侧栏只按它报告选择，不认识页面。
    public let pageIndex: Int
    public let title: String
    public let symbol: String
    public init(pageIndex: Int, title: String, symbol: String) {
      self.pageIndex = pageIndex
      self.title = title
      self.symbol = symbol
    }
  }
  public var onSelect: ((Int) -> Void)?
  private var buttons: [PaxNavigationButton] = []
  public init(title: String, primaryItems: [Item], secondaryItems: [Item]) {
    super.init(frame: .zero)
    wantsLayer = true
    let brandRow = PaxText.label(title, font: PaxTheme.Navigation.brandFont)
    brandRow.alignment = .center
    let navigation = NSStackView()
    navigation.orientation = .vertical
    navigation.spacing = PaxTheme.Navigation.itemGap
    navigation.alignment = .leading
    for item in primaryItems {
      let button = PaxNavigationButton(
        title: item.title, symbol: item.symbol, target: self, action: #selector(navigate(_:)))
      button.tag = item.pageIndex
      buttons.append(button)
      navigation.addArrangedSubview(button)
      button.widthAnchor.constraint(equalTo: navigation.widthAnchor).isActive = true
    }
    let secondary = NSStackView()
    secondary.orientation = .vertical
    secondary.alignment = .leading
    secondary.spacing = PaxTheme.Navigation.itemGap
    for item in secondaryItems {
      let button = PaxNavigationButton(
        title: item.title, symbol: item.symbol, target: self, action: #selector(navigate(_:)))
      button.tag = item.pageIndex
      buttons.append(button)
      secondary.addArrangedSubview(button)
      button.widthAnchor.constraint(equalTo: secondary.widthAnchor).isActive = true
    }
    let stack = NSStackView(views: [brandRow, navigation, NSView(), secondary])
    stack.orientation = .vertical
    stack.alignment = .leading
    stack.spacing = PaxTheme.Navigation.sectionGap
    stack.setCustomSpacing(PaxTheme.Navigation.brandGap, after: brandRow)
    stack.translatesAutoresizingMaskIntoConstraints = false
    addSubview(stack)
    NSLayoutConstraint.activate([
      stack.leadingAnchor.constraint(
        equalTo: leadingAnchor, constant: PaxTheme.Navigation.horizontalInset),
      stack.trailingAnchor.constraint(
        equalTo: trailingAnchor, constant: -PaxTheme.Navigation.horizontalInset),
      stack.topAnchor.constraint(
        equalTo: safeAreaLayoutGuide.topAnchor, constant: PaxTheme.Navigation.topInset),
      stack.bottomAnchor.constraint(
        equalTo: bottomAnchor, constant: -PaxTheme.Navigation.bottomInset),
      brandRow.widthAnchor.constraint(equalTo: stack.widthAnchor),
      navigation.widthAnchor.constraint(equalTo: stack.widthAnchor),
      secondary.widthAnchor.constraint(equalTo: stack.widthAnchor),
    ])
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var wantsUpdateLayer: Bool { true }
  public override func updateLayer() {
    layer?.backgroundColor = PaxTheme.Navigation.background.cgColor
  }
  public override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }
  public func select(_ pageIndex: Int) {
    for button in buttons { button.isSelected = button.tag == pageIndex }
  }
  @objc private func navigate(_ sender: NSButton) { onSelect?(sender.tag) }
}
