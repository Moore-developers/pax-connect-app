import AppKit

/// 浮层骨架，概览的标题、正文和操作由呈现容器一起滚动。
public final class PaxDialogView: NSView {
  public enum Style { case detail, auxiliary, event, compact, overview }
  public let style: Style
  public var onContentResize: (() -> Void)?
  public var naturalHeight: CGFloat {
    header.fittingSize.height + PaxTheme.Routes.pageInset + headerBodyGap
      + content.fittingSize.height + bodyTopInset + bodyBottomInset + footerBottomInset
      + (footer.isHidden ? 0 : footerHeight?.constant ?? 0)
  }
  private let titleField = PaxText.label(
    "", font: PaxTheme.Typography.label(size: PaxTheme.Routes.detailTitleFont, weight: .semibold))
  private let subtitleField = PaxText.label(
    "", font: PaxTheme.Typography.caption(), secondary: true)
  public let identity = NSStackView()
  public let identityActions = NSStackView()
  public let content = NSStackView()
  public let actions = NSStackView()
  public let header = NSStackView()
  public let status = PaxText.label(
    "", font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  public let bodyScroll = NSScrollView()
  private let footer = NSStackView()
  private var iconView: NSView?
  private weak var titleRow: NSStackView?
  private let body = RouteBodyClip()
  private let document = DialogDocument()
  private var footerHeight: NSLayoutConstraint?
  private var headerBodyGap: CGFloat { style == .auxiliary ? PaxTheme.Routes.bodyTop : 0 }
  private var bodyTopInset: CGFloat {
    style == .overview ? PaxTheme.Spacing.medium / 2 : PaxTheme.Routes.bodyTop
  }
  private var bodyBottomInset: CGFloat {
    style == .overview ? PaxTheme.Spacing.xs / 2 : PaxTheme.Routes.bodyTop
  }
  private var footerBottomInset: CGFloat { style == .overview ? PaxTheme.Routes.footerInset : 0 }
  private var footerRowHeight: CGFloat {
    PaxTheme.Routes.controlHeight + (style == .overview ? 0 : PaxTheme.Routes.footerInset * 2)
  }
  public init(
    title: String, subtitle: String = "", style: Style = .detail,
    symbol: String = "point.3.connected.trianglepath.dotted", tone: PaxFeatureTag.Tone = .blue
  ) {
    self.style = style
    super.init(frame: .zero)
    wantsLayer = true
    layer?.backgroundColor =
      (style == .overview ? PaxTheme.Color.workspace : PaxTheme.Routes.card).cgColor
    layer?.cornerRadius = PaxTheme.Routes.dialogRadius
    layer?.borderColor = PaxTheme.Routes.edge.cgColor
    layer?.borderWidth = PaxTheme.Layout.cardBorder
    layer?.masksToBounds = true
    titleField.font = PaxTheme.Typography.label(
      size: style == .event
        ? PaxTheme.Logs.detailTitle
        : style == .detail ? PaxTheme.Routes.detailTitleFont : PaxTheme.Routes.auxiliaryTitleFont,
      weight: style == .detail ? .semibold : .medium)
    if style == .overview {
      titleField.font = PaxTheme.Typography.label(
        size: PaxTheme.Dashboard.pageTitle, weight: .medium)
    }
    titleField.lineBreakMode = .byTruncatingTail
    titleField.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    titleField.stringValue = title
    subtitleField.stringValue = subtitle
    subtitleField.isHidden = subtitle.isEmpty
    identity.orientation = .vertical
    identity.alignment = .leading
    identity.spacing = PaxTheme.Spacing.xs
    identity.addArrangedSubview(titleField)
    identity.addArrangedSubview(subtitleField)
    identityActions.spacing = PaxTheme.Spacing.tight
    let icon = PaxRouteIconTile(
      symbol, tone: tone,
      size: style == .detail ? PaxTheme.Routes.headerIcon : PaxTheme.Routes.pickerIcon)
    let titleRow = NSStackView(
      views: style == .event || style == .compact || style == .overview
        ? [identity, NSView(), identityActions] : [icon, identity, NSView(), identityActions])
    if style == .event {
      titleRow.alignment = .top
      identityActions.topAnchor.constraint(equalTo: identity.topAnchor).isActive = true
      identityActions.heightAnchor.constraint(equalToConstant: PaxTheme.Routes.controlHeight)
        .isActive = true
    }
    titleRow.spacing = PaxTheme.Spacing.regular
    if style != .event { iconView = icon }
    self.titleRow = titleRow
    header.orientation = .vertical
    header.alignment = .leading
    header.spacing = style == .overview ? PaxTheme.Spacing.xs / 2 : PaxTheme.Spacing.medium
    header.addArrangedSubview(titleRow)
    content.orientation = .vertical
    content.alignment = .leading
    content.spacing = style == .compact ? PaxTheme.Spacing.medium : PaxTheme.Routes.sectionGap
    document.translatesAutoresizingMaskIntoConstraints = false
    content.translatesAutoresizingMaskIntoConstraints = false
    document.addSubview(content)
    let bodyView: NSView
    if style == .overview {
      bodyView = document
    } else {
      bodyScroll.contentView = body
      body.color = style == .detail ? PaxTheme.Routes.canvas : PaxTheme.Routes.card
      bodyScroll.drawsBackground = false
      bodyScroll.automaticallyAdjustsContentInsets = false
      bodyScroll.hasVerticalScroller = true
      bodyScroll.autohidesScrollers = true
      bodyScroll.documentView = document
      document.widthAnchor.constraint(equalTo: body.widthAnchor).isActive = true
      bodyView = bodyScroll
    }
    actions.orientation = .horizontal
    actions.spacing = PaxTheme.Spacing.tight
    footer.orientation = .horizontal
    footer.spacing = PaxTheme.Spacing.tight
    footer.addArrangedSubview(status)
    footer.addArrangedSubview(NSView())
    footer.addArrangedSubview(actions)
    for v in [header, bodyView, footer] {
      v.translatesAutoresizingMaskIntoConstraints = false
      addSubview(v)
    }
    let c = PaxTheme.Routes.self
    let headerInset =
      style == .compact || style == .overview
      ? PaxTheme.Spacing.page
      : style == .event ? PaxTheme.Logs.detailInset : style == .detail ? c.bodyInset : c.dialogInset
    let bodyEnd = bodyView.bottomAnchor.constraint(equalTo: footer.topAnchor)
    if style == .overview {
      // The presenter measures the body at its natural height before sizing the panel.
      bodyEnd.priority = .fittingSizeCompression
    }
    NSLayoutConstraint.activate([
      header.leadingAnchor.constraint(equalTo: leadingAnchor, constant: headerInset),
      header.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -headerInset),
      header.topAnchor.constraint(equalTo: topAnchor, constant: c.pageInset),
      titleRow.widthAnchor.constraint(equalTo: header.widthAnchor),
      bodyView.leadingAnchor.constraint(equalTo: leadingAnchor),
      bodyView.trailingAnchor.constraint(equalTo: trailingAnchor),
      bodyView.topAnchor.constraint(equalTo: header.bottomAnchor, constant: headerBodyGap),
      bodyEnd,
      footer.leadingAnchor.constraint(equalTo: leadingAnchor, constant: headerInset),
      footer.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -headerInset),
      footer.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -footerBottomInset),
      content.leadingAnchor.constraint(equalTo: document.leadingAnchor, constant: c.bodyInset),
      content.trailingAnchor.constraint(equalTo: document.trailingAnchor, constant: -c.bodyInset),
      content.topAnchor.constraint(equalTo: document.topAnchor, constant: bodyTopInset),
      content.bottomAnchor.constraint(equalTo: document.bottomAnchor, constant: -bodyBottomInset),
    ])
    if style == .event {
      content.spacing = PaxTheme.Logs.detailSection
      let separator = NSBox()
      separator.boxType = .separator
      separator.translatesAutoresizingMaskIntoConstraints = false
      addSubview(separator)
      NSLayoutConstraint.activate([
        separator.leadingAnchor.constraint(equalTo: leadingAnchor),
        separator.trailingAnchor.constraint(equalTo: trailingAnchor),
        separator.bottomAnchor.constraint(equalTo: footer.topAnchor),
      ])
    }
    footerHeight = footer.heightAnchor.constraint(equalToConstant: footerRowHeight)
    footerHeight?.isActive = true
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override func viewDidMoveToWindow() {
    super.viewDidMoveToWindow()
    updateOverviewCanvas()
  }
  public override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    updateOverviewCanvas()
  }
  private func updateOverviewCanvas() {
    if style == .overview {
      effectiveAppearance.performAsCurrentDrawingAppearance {
        layer?.backgroundColor = PaxTheme.Color.workspace.cgColor
      }
    }
  }
  public override var isFlipped: Bool { true }
  /// 用调用方提供的对象图标替换默认符号块；视图按标题图标尺寸固定。
  public func replaceIcon(_ view: NSView) {
    guard let titleRow, let old = iconView else { return }
    let side = style == .detail ? PaxTheme.Routes.headerIcon : PaxTheme.Routes.pickerIcon
    view.translatesAutoresizingMaskIntoConstraints = false
    titleRow.insertArrangedSubview(view, at: 0)
    titleRow.removeArrangedSubview(old)
    old.removeFromSuperview()
    NSLayoutConstraint.activate([
      view.widthAnchor.constraint(equalToConstant: side),
      view.heightAnchor.constraint(equalToConstant: side),
    ])
    iconView = view
  }
  public func applyTitle(_ title: String) { titleField.stringValue = title }
  public func applySubtitle(_ subtitle: String) { subtitleField.stringValue = subtitle }
  public func applyTitleColor(_ color: NSColor) { titleField.textColor = color }
  public func add(_ view: NSView) {
    content.addArrangedSubview(view)
    view.widthAnchor.constraint(equalTo: content.widthAnchor).isActive = true
    invalidateContentSize()
  }
  /// 已挂载子内容增减或隐藏后，通知呈现容器重新测量。
  private var contentUpdateDepth = 0
  private var pendingResize = false
  public func performContentUpdates(_ update: () -> Void) {
    contentUpdateDepth += 1
    update()
    contentUpdateDepth -= 1
    if contentUpdateDepth == 0 && pendingResize {
      pendingResize = false
      onContentResize?()
    }
  }
  public func invalidateContentSize() {
    if contentUpdateDepth > 0 { pendingResize = true } else { onContentResize?() }
  }
  public func clear() {
    for v in content.arrangedSubviews {
      content.removeArrangedSubview(v)
      v.removeFromSuperview()
    }
    invalidateContentSize()
  }
  public func setActions(_ buttons: [NSButton]) {
    for v in actions.arrangedSubviews {
      actions.removeArrangedSubview(v)
      v.removeFromSuperview()
    }
    for b in buttons { actions.addArrangedSubview(b) }
    let hidden = buttons.isEmpty && status.stringValue.isEmpty
    status.isHidden = status.stringValue.isEmpty
    footer.isHidden = hidden
    footerHeight?.constant = hidden ? 0 : footerRowHeight
    if style != .overview { bodyScroll.contentView.scroll(to: .zero) }
    invalidateContentSize()
  }
}
private final class DialogDocument: NSView { override var isFlipped: Bool { true } }
private final class RouteBodyClip: NSClipView {
  var color = PaxTheme.Routes.card { didSet { needsDisplay = true } }
  override func draw(_ dirtyRect: NSRect) {
    color.setFill()
    dirtyRect.fill()
  }
}
