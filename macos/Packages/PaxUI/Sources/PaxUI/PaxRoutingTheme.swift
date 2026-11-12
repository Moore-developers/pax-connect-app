import AppKit

extension PaxTheme {
  /// 分流仅补页面布局角色；外观、字号和行密度沿用现有工作台。
  public enum Routing {
    public static let emptyRulesHeight: CGFloat = 100
    public static let columns: [CGFloat] = [0.15, 0.23, 0.19, 0.17, 0.07, 0.19]
    public static let ruleColumns: [CGFloat] = [0.45, 0.25, 0.30]
    public static let priorityColumns: [CGFloat] = [0.34, 0.20, 0.22, 0.24]
    public static let librarySidebar: CGFloat = 200
    public static let libraryInset: CGFloat = 16
    public static let sidebarInset: CGFloat = 12
    public static let packageRow: CGFloat = 68
    public static let fixedPackageRow: CGFloat = 48
    public static let packageTextGap: CGFloat = 2
    public static let ruleInset: CGFloat = 6
    public static let ruleNumberWidth: CGFloat = 40
    public static let ruleHeader: CGFloat = 36
    public static let routeInset: CGFloat = 10
    public static let routeRadius: CGFloat = 7
    public static let moveSize: CGFloat = 18
    public static let moveHeight: CGFloat = 12
    public static let ruleWidth: CGFloat = 148
    public static let arrowPadding: CGFloat = 1
    public static let minimumTable: CGFloat = 120
  }
}
