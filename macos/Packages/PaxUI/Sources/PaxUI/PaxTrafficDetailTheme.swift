import AppKit

extension PaxTheme.Trend {
  public static let latencyCompactHeight: CGFloat = 22
  public static let latencyCompactMinimumSamples = 3
  public static let latencyCompactMaximumSamples = 8
}

extension PaxTheme {
  /// 流量详情只补布局角色；材质、颜色、字号和控件沿用工作台。
  public enum TrafficDetail {
    public static let latencyReadoutHeight: CGFloat = 56
    public static let latencyValueFont: CGFloat = 24
    public static let ruleRowHeight: CGFloat = 48
    public static let size = NSSize(width: 704, height: 720)
    public static let chartHeight: CGFloat = 118
    public static let relatedColumns: [CGFloat] = [0.60, 0.16, 0.24]
    public static let basisColumns: [CGFloat] = [0.2, 0.42, 0.26, 0.12]
    public static let connectionColumns: [CGFloat] = [0.18, 0.25, 0.17, 0.18, 0.22]
    public static let visibleConnections = 5
    public static let visibleRelated = 4
    public static let visibleBasis = 4
  }
}
