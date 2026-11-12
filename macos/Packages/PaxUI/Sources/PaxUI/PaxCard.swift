import AppKit

/// 实底内容卡片；内边距只由本视图应用一次，替换内容时释放旧约束。
public class PaxCard: NSView {
  public enum Style { case standard, routesSection, routesList, inset, currentRoute }
  public var clipsContent = false {
    didSet {
      let clipped = clipsContent || style == .routesList
      clipsToBounds = clipped
      layer?.masksToBounds = clipped
      layer?.mask = clipped ? roundedMask : nil
      needsLayout = true
    }
  }
  private let style: Style
  private let roundedMask = CAShapeLayer()
  private let highlight = PaxInnerHighlight()
  private let insets: NSEdgeInsets
  private var contentConstraints: [NSLayoutConstraint] = []
  public var contentView: NSView? {
    didSet {
      NSLayoutConstraint.deactivate(contentConstraints)
      oldValue?.removeFromSuperview()
      contentConstraints = []
      guard let contentView else { return }
      addSubview(contentView)
      contentView.translatesAutoresizingMaskIntoConstraints = false
      contentConstraints = [
        contentView.leadingAnchor.constraint(equalTo: leadingAnchor, constant: insets.left),
        contentView.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -insets.right),
        contentView.topAnchor.constraint(equalTo: topAnchor, constant: insets.top),
        contentView.bottomAnchor.constraint(
          lessThanOrEqualTo: bottomAnchor, constant: -insets.bottom),
      ]
      NSLayoutConstraint.activate(contentConstraints)
    }
  }

  public init(
    insets: NSEdgeInsets? = nil,
    style: Style = .standard
  ) {
    self.style = style
    self.insets =
      insets
      ?? (style == .routesSection
        ? NSEdgeInsets(
          top: PaxTheme.Routes.sectionTop, left: PaxTheme.Routes.sectionInset,
          bottom: PaxTheme.Routes.sectionTop, right: PaxTheme.Routes.sectionInset)
        : NSEdgeInsets(
          top: PaxTheme.Spacing.card, left: PaxTheme.Spacing.card,
          bottom: PaxTheme.Spacing.card, right: PaxTheme.Spacing.card))
    super.init(frame: .zero)
    wantsLayer = true
    clipsToBounds = style == .routesList
    layer?.masksToBounds = style == .routesList
    addSubview(highlight)
    if style == .routesList { layer?.mask = roundedMask }
    refreshSurface()
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }

  private func refreshSurface() {
    effectiveAppearance.performAsCurrentDrawingAppearance {
      layer?.backgroundColor =
        (style == .currentRoute
        ? PaxTheme.CurrentRoute.surface
        : style == .inset
          ? PaxTheme.Routes.canvas
          : style == .standard ? PaxTheme.Color.card : PaxTheme.Routes.card)
        .cgColor
      layer?.borderColor =
        (style == .currentRoute
        ? PaxTheme.CurrentRoute.line
        : style == .standard ? PaxTheme.Color.cardBorder : PaxTheme.Routes.edge).cgColor
      layer?.borderWidth =
        style == .inset
        ? 0
        : NSWorkspace.shared.accessibilityDisplayShouldIncreaseContrast
          ? 1 : PaxTheme.Layout.cardBorder
      layer?.cornerRadius =
        style == .currentRoute
        ? PaxTheme.CurrentRoute.radius
        : style == .inset
          ? PaxTheme.Radius.control
          : style == .standard
            ? PaxTheme.Radius.card
            : style == .routesList ? PaxTheme.Routes.tableRadius : PaxTheme.Routes.sectionRadius
      layer?.shadowColor = PaxTheme.Color.shadow.cgColor
      layer?.shadowOpacity = style == .inset ? 0 : Float(PaxTheme.Shadow.cardOpacity)
      layer?.shadowRadius = PaxTheme.Shadow.cardRadius
      layer?.shadowOffset = PaxTheme.Shadow.cardOffset
    }
  }
  public override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    refreshSurface()
  }
  public override func layout() {
    super.layout()
    if style == .routesList || clipsContent {
      roundedMask.frame = bounds
      roundedMask.path = CGPath(
        roundedRect: bounds, cornerWidth: layer?.cornerRadius ?? PaxTheme.Radius.card,
        cornerHeight: layer?.cornerRadius ?? PaxTheme.Radius.card, transform: nil)
    }
    highlight.isHidden = style == .routesList || style == .inset
    let path = CGPath(
      roundedRect: bounds, cornerWidth: layer?.cornerRadius ?? PaxTheme.Radius.card,
      cornerHeight: layer?.cornerRadius ?? PaxTheme.Radius.card, transform: nil)
    if layer?.shadowPath != path { layer?.shadowPath = path }
    if highlight.frame != bounds { highlight.frame = bounds }
  }
  public override func updateLayer() { refreshSurface() }
}
