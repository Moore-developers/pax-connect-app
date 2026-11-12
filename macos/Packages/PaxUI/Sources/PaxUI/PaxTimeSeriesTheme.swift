import AppKit

extension PaxTheme {
  /// Tokens for the shared time-series chart (SCT). Colors reuse DashboardColor and Routes.
  public enum TimeSeries {
    /// Minimum horizontal distance between two axis labels, endpoints included.
    public static let minimumTickSpacing: CGFloat = 56
    /// Latency axes never zoom below this ceiling, so 40-43 ms cannot fill the height.
    public static let latencyMinimumCeiling = 100.0
    /// Traffic axes use decimal MB; the floor only matters for an all-zero series.
    public static let trafficMinimumCeiling = 0.01
    /// Compact rows keep at most this many time slots.
    public static let compactMaximumSlots = 24
    public static let gridLineCount = 3
    public static let bridgeLineWidth: CGFloat = 1
    public static let bridgeOpacity: CGFloat = 0.55
    public static let isolatedDotRadius: CGFloat = 2.5
    /// How long a pinned bucket stays restorable while its chart is rebuilt.
    public static let pinRestoreWindow: TimeInterval = 2
    public static let unitFont: CGFloat = 11
    /// Traffic-list latency column (SCT-09): whitespace between the compact chart's drawn right
    /// edge (plot edge plus endpoint dot) and the left edge of the latest-value text. One third
    /// of the former visible gap, which averaged 40 pt for "1.42 s" (43.6) and "134 ms" (36.7).
    public static let compactValueGap: CGFloat = 13
    /// How far the compact chart's drawn line and endpoint dot sit inside its frame on the right.
    public static var compactDrawnTrailingInset: CGFloat {
      PaxTheme.Trend.inset - PaxTheme.ChartPlot.endpointRadius
    }
  }
}
