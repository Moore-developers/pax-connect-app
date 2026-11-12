import AppKit

extension PaxTheme {
  /// 所有数据表共用外观；密度只改变行高与表头高度。
  public enum Table {
    public static let row: CGFloat = 64
    public static let compactRow: CGFloat = 44
    public static let memberRow: CGFloat = 48
    public static let header: CGFloat = 44
    public static let compactHeader: CGFloat = 36
    public static let inset: CGFloat = 12
    public static let font: CGFloat = 13
    public static let radius: CGFloat = 12
    public static let selectionInset: CGFloat = 4
    public static let selectionRadius: CGFloat = 8
    public static let fill = PaxTheme.Routes.card
    public static let headerFill = PaxTheme.Routes.card
    public static let line = PaxTheme.Routes.line
    public static let text = PaxTheme.Routes.text
    public static let muted = PaxTheme.Routes.muted
    public static let selected = PaxTheme.Routes.menuFill
    public static let rail = PaxTheme.Routes.blue
  }
  public enum Groups {
    public static let sidebar: CGFloat = 176
    public static let sidebarMinimum: CGFloat = 150
    public static let bodyHeight: CGFloat = 396
    public static let gap: CGFloat = 16
    public static let searchWidth: CGFloat = 300
    public static let connectionWidth: CGFloat = 162
    public static let featureWidth: CGFloat = 96
    public static let namePopoverWidth: CGFloat = 320
  }
}
