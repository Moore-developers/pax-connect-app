import AppKit

/// 通用图表的系列色与绘制尺寸；PaxTheme.Chart 的基础值留在 PaxTheme.swift。
extension PaxTheme.Chart {
  // 用量类图表的语义系列色：输入、输出、缓存读取、缓存写入、请求成功与失败。
  // 柱条参考配色：缓存翠绿、输入橙、输出浅蓝、其他灰。
  private static func fixed(_ hex: UInt32) -> NSColor {
    NSColor(
      srgbRed: CGFloat((hex >> 16) & 255) / 255, green: CGFloat((hex >> 8) & 255) / 255,
      blue: CGFloat(hex & 255) / 255, alpha: 1)
  }
  private static func adaptive(_ name: String, light: UInt32, dark: UInt32) -> NSColor {
    NSColor(name: NSColor.Name(name)) { appearance in
      fixed(appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua ? dark : light)
    }
  }
  public static let inputSeries = fixed(0xf0a35e)
  public static let outputSeries = fixed(0x9ec5f0)
  /// CLI 品牌系列色，与输入 / 输出等用量语义色独立。
  public static let codexSeries = fixed(0x5268ff)
  public static let deepSeekSeries = fixed(0x8854d9)
  public static let cacheSeries = PaxTheme.DashboardColor.green
  public static let cacheWriteSeries = fixed(0xb5b5b5)
  /// 文字用的系列色：浅色下更深、深色下更亮，保证对比度。
  public static let cacheLabel = adaptive("PaxCacheLabel", light: 0x2459b0, dark: 0x7fb0f0)
  public static let requestSeries = [PaxTheme.DashboardColor.green, PaxTheme.DashboardColor.red]
  public static let categorySeries = [inputSeries, cacheSeries, cacheWriteSeries, outputSeries]

  /// Session analysis: total / ordinary input / output / cache read / cache write.
  public static let analysisCostSeries = [
    PaxTheme.Color.text, inputSeries, PaxTheme.DashboardColor.red, cacheLabel, cacheSeries,
  ]
  /// 缓存趋势：蓝色Token曲线、绿色缓存费用、橙色其余输入费用。
  public static let analysisRequestSeries = [
    adaptive("PaxRequestCacheRead", light: 0x168bff, dark: 0x4fa7ff),
    cacheSeries,
    inputSeries,
  ]
  public static let analysisGrid = adaptive("PaxAnalysisGrid", light: 0xd2d7df, dark: 0x50565f)
  public static let analysisLinkedHeight: CGFloat = 336
  public static let analysisStackedAreaOpacity: CGFloat = 0.5
  public static let analysisLegendSwatch: CGFloat = 12
  public static let analysisLegendDot: CGFloat = 7
  public static let analysisRequestDash: [CGFloat] = [4, 3]
  public static let analysisAnnotationLineOpacity: CGFloat = 0.55
  public static let analysisAnnotationFillOpacity: CGFloat = 0.16
  public static let analysisAnnotationBackgroundOpacity: CGFloat = 0.78
  public static let analysisAnnotationStroke: CGFloat = 1
  public static let analysisAnnotationShadowOpacity: CGFloat = 0.1
  public static let analysisAnnotationShadowBlur: CGFloat = 6
  /// Hover card H1: width, padding, layer gap, radius and row height.
  public static let analysisHoverWidth: CGFloat = 260
  public static let analysisHoverPadding: CGFloat = 12
  public static let analysisHoverGap: CGFloat = 8
  public static let analysisHoverRadius: CGFloat = 12
  public static let analysisHoverRow: CGFloat = 17
  public static let analysisHoverTitle: CGFloat = 13
  public static let analysisHoverSmall: CGFloat = 11
  public static let analysisHoverMetric: CGFloat = 24
  public static let analysisHoverShadowOpacity: CGFloat = 0.12
  public static let analysisHoverShadowBlur: CGFloat = 14
  public static let analysisTokenSeries = [
    inputSeries, PaxTheme.DashboardColor.red, cacheLabel, cacheSeries,
  ]
  public static let analysisHeight: CGFloat = 260
  public static let analysisLegendHeight: CGFloat = 28
  public static let analysisMarker: CGFloat = 3
  public static let analysisRequestStroke: CGFloat = 2

  /// 单行份额条（PaxShareBar）的高度。
  public static let shareBarHeight: CGFloat = 6
  /// 紧凑比例条（PaxProportionView.compact）的整体、色条与图例行位置。
  public static let proportionCompactHeight: CGFloat = 32
  public static let proportionCompactBar: CGFloat = 8
  public static let proportionCompactLabel: CGFloat = 16

  /// 斜纹填充：叠在系列色上标示“部分占比”。
  public static let stripePeriod: CGFloat = 4
  public static let stripeWidth: CGFloat = 2
  public static let stripeOpacity: CGFloat = 0.26
  public static let stripeColor = NSColor.white

  /// 漏斗柱（PaxStackedChart.Style.funnel）：左侧刻度列、顶部留白、底部时间轴、柱宽占列宽比例与悬停列高亮不透明度。
  public static let funnelAxis: CGFloat = 44
  public static let funnelTop: CGFloat = 6
  public static let funnelFooter: CGFloat = 18
  public static let funnelBarFraction: CGFloat = 0.66 * 2 / 3
  public static let funnelTopRadius = barTopRadius + 2
  /// 用量趋势：柱宽按至少24列计算，取列宽的3/4；日期位置可独立平铺。
  public static let unitCostSeries = fixed(0x6366f1)
  public static let usageMinimumColumns = 24
  public static let usageBarFraction: CGFloat = 3 / 4
  public static let funnelColumnOpacity: CGFloat = 0.06
  /// 列式柱图（Style.labeledColumns）：列顶表头（日期 + 金额）高度与两行文字的上沿、金额字号、柱左右内缩、
  /// 最高柱占柱区高度的比例；列间分隔线宽。
  public static let labeledHeader: CGFloat = 40
  public static let labeledDateY: CGFloat = 8
  public static let labeledAmountY: CGFloat = 22
  public static let labeledDateFont: CGFloat = 11
  public static let labeledAmountFont: CGFloat = 13
  public static let labeledBarInset: CGFloat = 2
  public static let labeledPeakFraction: CGFloat = 0.68
  public static let labeledDivider: CGFloat = 1
  /// 概览摘要柱：柱顶总量、底部日期/小时、当前时段浅底。
  public static let summaryTop: CGFloat = 24
  public static let summaryFooter: CGFloat = 24
  public static let summaryBarFraction: CGFloat = 0.58
  /// 今日流量最近12个小时的固定柱宽。
  public static let trafficSummaryBarWidth: CGFloat = 20
  public static let summaryLabelWidth: CGFloat = 48
  public static let summaryCurrentOpacity: CGFloat = 0.035
  /// 悬停整列的浅蓝底（强调色）不透明度。
  public static let labeledHoverOpacity: CGFloat = 0.08
  /// 列式柱图的悬停浮层：单行胶囊的内边距、段间距、行高、圆角，以及与柱顶之间的引线长度。
  public static let tipPaddingX: CGFloat = 9
  public static let tipPaddingY: CGFloat = 5
  public static let tipGap: CGFloat = 8
  public static let tipLine: CGFloat = 17
  public static let tipRadius: CGFloat = 8
  public static let tipStem: CGFloat = 8

  /// 圆环（PaxDonutChart）直径与线宽。
  /// 分类堆叠柱左侧刻度列宽（0 / 中间值 / 最大值）。
  public static let categoricalAxis: CGFloat = 44

  public static let donutSize: CGFloat = 104
  public static let donutStroke: CGFloat = 13
  public static let compactDonutSize: CGFloat = 56
  public static let compactDonutStroke: CGFloat = 5
}

extension PaxTheme.Control {
  /// 自绘控件的键盘焦点环与禁用态透明度。
  public static let focusRingWidth: CGFloat = 2
  public static let focusRingInset: CGFloat = 1
  public static let disabledOpacity: CGFloat = 0.4
}
