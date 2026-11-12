import AppKit

extension PaxTheme {
  /// 全局侧栏唯一外观与布局角色，各页面不覆盖导航主题。
  public enum Navigation {
    public static let width = Layout.sidebar
    public static let widthRange = Layout.sidebarRange
    public static var font: NSFont { Typography.label() }
    public static var brandFont: NSFont { Typography.brand }
    public static let background = Color.sidebar
    public static let text = Color.text
    public static let selected = Color.selected
    public static let selectedText = Color.selectedText
    public static let hover = Color.navigationHover
    public static let rowHeight = Layout.navigationRow
    public static let radius = Radius.control
    public static let iconSize = Control.navigationIcon
    public static let iconGap = Control.navigationIconGap
    public static let contentInset = Control.horizontalPadding
    public static let horizontalInset = Spacing.medium
    /// 品牌行上下留白相对基准值的放大系数。
    private static let brandRowScale: CGFloat = 1.2
    private static let brandLift: CGFloat = 2
    public static let topInset = Spacing.pageTop / 2 * brandRowScale - brandLift
    /// 补回标题上移的距离，保持首个导航项位置不变。
    public static let brandGap = Dashboard.navGap * brandRowScale + brandLift
    public static let bottomInset = Spacing.card
    public static let itemGap = Spacing.tight
    public static let sectionGap = Dashboard.navGap
    public static let shadowOpacity = Control.selectedShadowOpacity
    public static let shadowRadius = Control.selectedShadowRadius
    public static let shadowOffset = Control.selectedShadowOffset
  }
}
