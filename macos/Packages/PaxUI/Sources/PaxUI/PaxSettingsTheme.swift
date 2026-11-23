import AppKit

extension PaxTheme {
  /// 设置页独有的布局尺寸；文字、颜色、分组及控件沿用既有角色。
  public enum Settings {
    public static let rowMinimum: CGFloat = 64
    public static let compactRowMinimum: CGFloat = 56
    public static let compactRowInset: CGFloat = 12
    public static let compactHorizontalInset: CGFloat = 18
    public static let storageReadoutWidth: CGFloat = 280
    public static let pickerWidth: CGFloat = 155
    public static let numberWidth: CGFloat = 58
    public static let footerHeight: CGFloat = 74
    public static let confirmationSize = NSSize(width: 465, height: 360)
  }
}
