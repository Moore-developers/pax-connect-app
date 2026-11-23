import AppKit

extension PaxTheme.Trend {
  public static let latencyCompactHeight: CGFloat = 22
  public static let latencyCompactMinimumSamples = 3
  public static let latencyCompactMaximumSamples = 8
}

extension PaxTheme {
  /// 流量详情只补布局角色；材质、颜色、字号和控件沿用工作台。
  public enum TrafficDetail {
    public static let hoverWidth: CGFloat = 460
    public static let hoverSubtleFill = Color.adaptive(
      "TargetHoverSubtleFill", light: 0xF6F6F7, dark: 0x313338)
    public static let hoverChipNeutralFill = Color.adaptive(
      "TargetHoverChipNeutral", light: 0xEFF0F2, dark: 0x35373D)
    public static let hoverDivider = Color.adaptive(
      "TargetHoverDivider", light: 0xE5E5E7, dark: 0x3C3E43)
    public static let hoverGreenText = Color.adaptive(
      "TargetHoverGreenText", light: 0x087F5B, dark: 0x34D399)
    public static let hoverAmberText = Color.adaptive(
      "TargetHoverAmberText", light: 0xA15C00, dark: 0xF2B84B)
    public static let hoverRedText = Color.adaptive(
      "TargetHoverRedText", light: 0xBC3445, dark: 0xFB7185)
    public static let hoverGreenFill = DashboardColor.green.withAlphaComponent(0.12)
    public static let hoverAmberFill = DashboardColor.amber.withAlphaComponent(0.14)
    public static let hoverRedFill = DashboardColor.red.withAlphaComponent(0.12)
    public static let hoverStripRadius: CGFloat = 12
    public static let hoverBlockRadius: CGFloat = 8
    public static let hoverPillRadius: CGFloat = 6
    public static let hoverLatencyChartHeight: CGFloat = 104
    public static let hoverExitRowHeight: CGFloat = 32
    public static let hoverBodyMinimumHeight: CGFloat = 140
    public static let hoverTabBodyMaxHeight: CGFloat = 260
    public static let hoverStatusDotSize: CGFloat = 6
    public static let hoverTitleFont: CGFloat = 15
    public static let hoverMetricFont: CGFloat = 14
    public static let hoverBodyFont: CGFloat = 12.5
    public static let hoverCodeFont: CGFloat = 11
    public static let hoverMetaFont: CGFloat = 11
    public static let latencyReadoutHeight: CGFloat = 56
    public static let latencyValueFont: CGFloat = 24
    public static let ruleRowHeight: CGFloat = 48
    public static let size = NSSize(width: 704, height: 720)
    public static let chartHeight: CGFloat = 118
    public static let relatedColumns: [CGFloat] = [0.60, 0.16, 0.24]
    public static let basisColumns: [CGFloat] = [0.2, 0.42, 0.26, 0.12]
    public static let targetColumns: [CGFloat] = [0.48, 0, 0.23, 0.29]
    public static let targetRowHeight: CGFloat = 52
    public static let targetFilterPickerWidth: CGFloat = 148
    public static let visibleRelated = 4
    public static let visibleBasis = 4
  }
}

extension PaxTheme {
  public enum HoverReadout {
    public static let bridgeDelay: Double = 0.3
    public static let arrowMargin: CGFloat = 24
    public static let cursorGap: CGFloat = 18
  }
}
