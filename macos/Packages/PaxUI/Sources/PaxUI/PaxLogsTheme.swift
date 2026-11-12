import AppKit

extension PaxTheme {
  /// 日志布局与级别语义；颜色复用工作台基础 token。
  public enum Logs {
    public static let alternateBackground = PaxTheme.Routes.canvas
    public static let errorText = PaxTheme.Color.error
    public static let warningText = PaxTheme.Color.warning
    public static let infoText = PaxTheme.Routes.blue
    public static let directoryWidth: CGFloat = 208
    public static let inspectorWidth: CGFloat = 360
    public static let inspectorThreshold: CGFloat = 1120
    public static let eventRow: CGFloat = 48
    public static let timeColumn: CGFloat = 132
    public static let durationColumn: CGFloat = 112
    public static let applicationColumn: CGFloat = 210
    public static let stateColumn: CGFloat = 150
    public static let connectionColumn: CGFloat = 150
    public static let transferColumn: CGFloat = 224
    public static let eventColumn: CGFloat = 250
    public static let targetColumn: CGFloat = 166
    public static let eventGrowth: CGFloat = 0.3
    public static let targetGrowth: CGFloat = 0.7
    public static let cellInset: CGFloat = 3
    public static let footerHeight: CGFloat = 40
    public static let toolsHeight: CGFloat = 60
    public static let directoryRow: CGFloat = 28
    public static let headerHeight: CGFloat = 34
    public static let directoryIconSize: CGFloat = 20
    public static let directoryCompactIconSize: CGFloat = 18
    public static let directoryFont: CGFloat = 12
    public static let directoryCountFont: CGFloat = 12
    public static let iconSize: CGFloat = 14
    public static let bodyFont: CGFloat = 12
    public static let titleFont: CGFloat = 13
    public static let detailTitle: CGFloat = 21
    public static let factLabel: CGFloat = 74
    public static let factGap: CGFloat = 16
    public static let detailInset: CGFloat = 24
    public static let calloutInset: CGFloat = 14
    public static let detailSection: CGFloat = 18
    public static let codeFont: CGFloat = 11
    public static let directoryIcon = PaxTheme.Routes.blue.withAlphaComponent(0.55)
    public static let directoryFill = PaxTheme.Routes.canvas
    public static let detailSize = NSSize(width: 630, height: 460)
  }
}
