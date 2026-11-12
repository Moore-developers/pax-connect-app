import AppKit

/// 玻璃层的统一封装（architecture.md §14.1）。
///
/// 业务代码不直接创建 `NSGlassEffectView`：包在这里以后可以统一调参或降级。
/// 两条来自 §14.1 与 S0-④ 实测的约束由本类型负责兜住：
///   1. 玻璃必须**包住**内容（作为 `contentView`），不能当背景垫在内容下面；
///   2. 玻璃不能叠玻璃——需要多块相邻玻璃时用 `GlassContainer`，由容器决定融合。
public final class GlassSurface: NSView {
  public enum Style: Sendable {
    case regular
    case clear

    fileprivate var glassStyle: NSGlassEffectView.Style {
      switch self {
      case .regular: .regular
      case .clear: .clear
      }
    }
  }

  private let glass = NSGlassEffectView()

  public var cornerRadius: CGFloat {
    get { glass.cornerRadius }
    set { glass.cornerRadius = newValue }
  }

  public var tintColor: NSColor? {
    get { glass.tintColor }
    set { glass.tintColor = newValue }
  }

  /// 内容视图；玻璃会把它嵌在自己的内容层里。
  public var contentView: NSView? {
    get { glass.contentView }
    set { glass.contentView = newValue }
  }

  public init(
    style: Style = .regular, cornerRadius: CGFloat = PaxTheme.Radius.glass,
    interactive: Bool = false
  ) {
    super.init(frame: .zero)
    glass.style = style.glassStyle
    glass.cornerRadius = cornerRadius
    if #available(macOS 27.0, *) { glass.effectIsInteractive = interactive }
    glass.translatesAutoresizingMaskIntoConstraints = false
    addSubview(glass)
    NSLayoutConstraint.activate([
      glass.leadingAnchor.constraint(equalTo: leadingAnchor),
      glass.trailingAnchor.constraint(equalTo: trailingAnchor),
      glass.topAnchor.constraint(equalTo: topAnchor),
      glass.bottomAnchor.constraint(equalTo: bottomAnchor),
    ])
  }

  @available(*, unavailable)
  public required init?(coder: NSCoder) {
    fatalError("PaxUI 不使用 XIB/Storyboard（code-standards.md §二）")
  }
}

/// 多块玻璃的容器：子玻璃之间距离小于 `spacing` 时由系统融合成连续的一块（§14.1、S0-④ 实测）。
public final class GlassContainer: NSView {
  private let container = NSGlassEffectContainerView()

  /// 融合距离：两块玻璃的间距小于它时会融合。
  public var spacing: CGFloat {
    get { container.spacing }
    set { container.spacing = newValue }
  }

  /// 玻璃块放进这里（作为容器的 `contentView` 的子视图）。
  public var surfaceHost: NSView? {
    get { container.contentView }
    set { container.contentView = newValue }
  }

  public init(spacing: CGFloat = 0) {
    super.init(frame: .zero)
    container.spacing = spacing
    container.translatesAutoresizingMaskIntoConstraints = false
    addSubview(container)
    NSLayoutConstraint.activate([
      container.leadingAnchor.constraint(equalTo: leadingAnchor),
      container.trailingAnchor.constraint(equalTo: trailingAnchor),
      container.topAnchor.constraint(equalTo: topAnchor),
      container.bottomAnchor.constraint(equalTo: bottomAnchor),
    ])
  }

  @available(*, unavailable)
  public required init?(coder: NSCoder) {
    fatalError("PaxUI 不使用 XIB/Storyboard（code-standards.md §二）")
  }
}
