import AppKit

extension PaxTheme.Control {
  /// Filter rows: search field, dropdowns and text buttons share this height.
  public static let filterHeight: CGFloat = 32
}

extension PaxTheme.Chart {
  /// Header of the gradient bar style: the unit line only, no readout block.
  public static let compactHeader: CGFloat = 20
  public static let barTopRadius: CGFloat = 4
  public static let barShade: CGFloat = 0.12
}

extension PaxTheme {
  public enum AISession {
    public static let pickerWidth: CGFloat = 520
    public static let pickerHeight: CGFloat = 420
    public static let pickerRowHeight: CGFloat = 56
    public static let pickerSearchWidth: CGFloat = 240
    public static let selectorWidth: CGFloat = 280
    public static let selectorHeight: CGFloat = PaxTheme.Control.filterHeight * 4 / 3
    public static let selectorChevronSize: CGFloat = PaxTheme.Routes.bodyFont + 2
    public static let contextRowHeight: CGFloat = 54
    public static let contextLineSpacing: CGFloat = PaxTheme.Spacing.tight / 2
    public static let costRankColors: [NSColor] = [
      DashboardColor.red.blended(withFraction: 0.18, of: .white) ?? DashboardColor.red,
      .systemOrange,
      DashboardColor.amber.blended(withFraction: 0.35, of: .white) ?? DashboardColor.amber,
    ]
  }
}
