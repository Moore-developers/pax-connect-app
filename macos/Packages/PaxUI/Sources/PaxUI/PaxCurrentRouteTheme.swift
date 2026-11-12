import AppKit

extension PaxTheme {
  /// 用户最新参考图的卡片内坐标（1492 × 784），字体与表面只在此定义。
  public enum CurrentRoute {
    public static let referenceWidth: CGFloat = 1492
    public static let referenceContent = NSRect(x: 52, y: 36, width: 1388, height: 704)
    public static let minimumFont: CGFloat = 11
    public static let radius = PaxTheme.Radius.card
    public static let surface = NSColor.white
    public static let line = NSColor(srgbRed: 0.89, green: 0.90, blue: 0.88, alpha: 1)
    public static let ink = NSColor(srgbRed: 0.12, green: 0.26, blue: 0.21, alpha: 1)
    public static let muted = NSColor(srgbRed: 0.52, green: 0.57, blue: 0.55, alpha: 1)
    public static let greenFill = NSColor(srgbRed: 0.90, green: 0.94, blue: 0.91, alpha: 1)
    public static let amber = NSColor(srgbRed: 1, green: 0.67, blue: 0, alpha: 1)
    public static let heading = (NSRect(x: 184, y: 53, width: 1000, height: 62), CGFloat(46))
    public static let badge = (NSRect(x: 1276, y: 47, width: 164, height: 62), CGFloat(30))
    public static let number = (NSRect(x: 66, y: 159, width: 240, height: 145), CGFloat(128))
    public static let unit = (NSRect(x: 316, y: 221, width: 100, height: 65), CGFloat(58))
    public static let delta = (NSRect(x: 70, y: 288, width: 158, height: 64), CGFloat(36))
    public static let comparison = (NSRect(x: 252, y: 299, width: 340, height: 50), CGFloat(34))
    public static let measured = (NSRect(x: 74, y: 378, width: 500, height: 45), CGFloat(30))
    public static let note = (NSRect(x: 74, y: 422, width: 500, height: 26), CGFloat(24))
    public static let protocolLabel = (NSRect(x: 695, y: 211, width: 230, height: 48), CGFloat(34))
    public static let proto = (NSRect(x: 945, y: 211, width: 495, height: 48), CGFloat(34))
    public static let exitLabel = (NSRect(x: 695, y: 283, width: 245, height: 48), CGFloat(34))
    public static let exitIP = (NSRect(x: 945, y: 283, width: 495, height: 48), CGFloat(34))
    public static let region = (NSRect(x: 695, y: 353, width: 745, height: 52), CGFloat(36))
    public static let axis = (NSRect(x: 58, y: 506, width: 650, height: 50), CGFloat(28))
    public static let chart = NSRect(x: 52, y: 520, width: 1388, height: 220)
    public static let dividers = [
      NSRect(x: 70, y: 136, width: 1370, height: 2),
      NSRect(x: 636, y: 174, width: 2, height: 260),
      NSRect(x: 52, y: 474, width: 1388, height: 2),
    ]
    public static func font(_ size: CGFloat) -> NSFont {
      PaxTheme.Typography.label(size: size, weight: .medium)
    }
  }
}
