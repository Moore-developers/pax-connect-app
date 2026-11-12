import AppKit

extension PaxTheme {
  /// Geometry of the approved workbench. Surfaces and colors remain Pax tokens.
  public enum NetworkDiagnostics {
    public static let pairColumns: [CGFloat] = [0.34, 0.66]
    public static let readingColumns: [CGFloat] = [0.24, 0.46, 0.30]
    public static let sourceColumns: [CGFloat] = [0.26, 0.13, 0.25, 0.14, 0.22]
    public static let serviceColumns: [CGFloat] = [0.30, 0.21, 0.49]
    public static let equalColumnRatio: CGFloat = 0.5
    public static let leftColumnRatio: CGFloat = 0.59
    public static let labelWidth: CGFloat = 138
    public static let sourceNameWidth: CGFloat = 120
    public static let sourceRegionWidth: CGFloat = 210
    public static let sourceASNWidth: CGFloat = 100
    public static let sourceQueryWidth: CGFloat = 210
    public static let sourceRowHeight: CGFloat = 60
    public static let heroExitRatio: CGFloat = 1.35
    public static let ipFont: CGFloat = 20
    public static let metricFont: CGFloat = 24
    public static let rowInset: CGFloat = 8
    public static let cardGap: CGFloat = 5
    public static let summaryCardInsets = NSEdgeInsets(
      top: PaxTheme.Spacing.tight, left: PaxTheme.Spacing.regular,
      bottom: PaxTheme.Spacing.tight, right: PaxTheme.Spacing.regular)
    public static let heroInset: CGFloat = 14
    public static let environmentIPFont: CGFloat = 28
    public static let environmentIcon: CGFloat = 18
    public static let environmentLabel: CGFloat = 58
    public static let environmentRowInset: CGFloat = 3
    public static let environmentHorizontalInset = PaxTheme.Routes.sectionInset / 2
    public static let environmentVerticalInset = PaxTheme.Routes.sectionTop / 2
    public static let environmentTopInset = environmentVerticalInset / 2
    public static let environmentCardInsets = NSEdgeInsets(
      top: environmentTopInset, left: environmentHorizontalInset,
      bottom: environmentVerticalInset, right: environmentHorizontalInset)
    public static let environmentSummaryRatio: CGFloat = 0.42
    public static let environmentScoreRatio: CGFloat = 0.36
    public static let environmentMetricInset: CGFloat = 8
    public static let environmentMetricHeight: CGFloat = 52
    public static let environmentScoreFont: CGFloat = 20
    public static let progressBackground = Color.adaptive(
      "PaxDiagnosticProgress", light: 0xf3f3f5, dark: 0x33353a
    ).withAlphaComponent(0.5)
    public static let progressInset: CGFloat = 14
    public static let progressRadius: CGFloat = 12
    public static let progressLabelWidth: CGFloat = 114
    public static let progressTimeWidth: CGFloat = 44
    public static let progressIcon: CGFloat = 16
    public static let progressRowInset: CGFloat = 7
    public static let progressRowHeight: CGFloat = 32
    public static let progressTitleFont: CGFloat = 14
    public static let progressBodyFont: CGFloat = 13
    public static let progressMetaFont: CGFloat = 12
    public static let progressBadgeFont: CGFloat = 11
    public static let progressCollapseSeconds = 10
    public static let progressTick: Duration = .seconds(1)
  }
}
