import AppKit

/// 连接工作台设计系统的唯一 token 来源；PaxUI 不依赖业务模型。
public enum PaxTheme {
  public enum Color {
    static func adaptive(_ name: String, light: UInt32, dark: UInt32) -> NSColor {
      NSColor(name: NSColor.Name(name)) { appearance in
        let hex = appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua ? dark : light
        return NSColor(
          srgbRed: CGFloat((hex >> 16) & 255) / 255,
          green: CGFloat((hex >> 8) & 255) / 255, blue: CGFloat(hex & 255) / 255, alpha: 1)
      }
    }
    // 用户选择的连接工作台灰阶；系统材质只用于窗口与控制层。
    public static let workspace = adaptive("PaxCanvas", light: 0xf3f3f4, dark: 0x202124)
    public static let sidebar = adaptive("PaxSidebar", light: 0xededf0, dark: 0x27282c)
    public static let card = adaptive("PaxCard", light: 0xfdfdfd, dark: 0x292a2d)
    public static let cardBorder = adaptive("PaxBorder", light: 0xe5e5e7, dark: 0x3c3e43)
    public static let text = adaptive("PaxText", light: 0x202124, dark: 0xf3f3f4)
    public static let textSecondary = adaptive("PaxSecondary", light: 0x73757a, dark: 0xb2b3ba)
    public static let textDisabled = NSColor.disabledControlTextColor
    public static let onSolid = NSColor.white
    public static let control = adaptive("PaxControl", light: 0xffffff, dark: 0x36383d)
    public static let navigationHover = adaptive(
      "PaxNavigationHover", light: 0xe3e3e6, dark: 0x36383d)
    public static let shadow = NSColor.black
    public static let selected = adaptive("PaxSelected", light: 0x303133, dark: 0xe8e8eb)
    public static let selectedText = adaptive("PaxSelectedText", light: 0xffffff, dark: 0x242529)
    public static let healthy = DashboardColor.greenLabel
    public static let warning = DashboardColor.uploadLabel
    public static let yellowLabel = adaptive("PaxYellowLabel", light: 0x8b7600, dark: 0xe4ce5b)
    public static let amberLabel = adaptive("PaxAmberLabel", light: 0xa15c00, dark: 0xf2b84b)
    public static let error = DashboardColor.redLabel
    public static let accent = NSColor.controlAccentColor
    public static let noticeFillOpacity: CGFloat = 0.08
    public static let series = [
      DashboardColor.green, DashboardColor.amber, textSecondary,
    ]
  }

  public enum InlineStatus {
    public static let width: CGFloat = 320
    public static let detailsWidth: CGFloat = 360
    public static let detailsMaximumHeight: CGFloat = 360
    public static let successDuration: Double = 3
  }

  public enum Radius {
    public static let control: CGFloat = 8
    public static let card: CGFloat = 20
    public static let glass: CGFloat = 18
  }

  public enum Spacing {
    public static let xs: CGFloat = 4
    public static let tight: CGFloat = 8
    public static let medium: CGFloat = 12
    public static let regular: CGFloat = 16
    public static let card: CGFloat = 16
    public static let roomy: CGFloat = 24
    public static let page: CGFloat = 20
    /// 标题栏安全区之后的留白；卡片间距横向和纵向同值。
    public static let pageTop: CGFloat = 8
    public static let cardGap: CGFloat = 8
  }

  public enum Layout {
    public static let minimumWindow = NSSize(width: 1000, height: 680)
    public static let defaultWindow = NSSize(width: 1280, height: 860)
    public static let sidebar: CGFloat = 146
    public static let wideContent: CGFloat = 700
    public static let tableRow: CGFloat = 48
    public static let cardBorder: CGFloat = 1
    public static let noticeIcon: CGFloat = 18
    public static let noticeMinimumText: CGFloat = 100
    public static let timeColumn: CGFloat = 46
    public static let sheet = NSSize(width: 640, height: 440)
    public static let galleryTable: CGFloat = 180
    public static let emptyState: CGFloat = 140
    public static let glassControl: CGFloat = 48
    public static let sidebarRange: ClosedRange<CGFloat> = 146...180
    public static let navigationRow: CGFloat = 36
    public static let metricMinimumLabel: CGFloat = 320
    public static let searchWidth: CGFloat = 220
  }

  public enum Chart {
    public static let height: CGFloat = 140
    public static let proportionHeight: CGFloat = 48
    public static let sparklineHeight: CGFloat = 80
    public static let header: CGFloat = 44
    public static let footer: CGFloat = 30
    public static let proportionGap: CGFloat = 2
    /// 今日流量占比条的高度与单段最小宽度。
    public static let trafficShareBar: CGFloat = 8
    public static let trafficShareMinimum: CGFloat = 3
    public static let proportionRadius: CGFloat = 5
    public static let proportionLegendInset: CGFloat = 12
    public static let proportionDot: CGFloat = 7
    public static let proportionDotOffset: CGFloat = 4
    public static let proportionBar: CGFloat = 22
    public static let proportionLabel: CGFloat = 32
    public static let lineWidth: CGFloat = 2
    public static let barGapFraction: CGFloat = 0.35
  }

  public enum Workbench {
    public static let mainHeight: CGFloat = 360
    public static let longMainHeight: CGFloat = 480
    public static let compactMainHeight: CGFloat = 330
    public static let compactLongHeight: CGFloat = 390
    public static let header: CGFloat = 44
    public static let details: CGFloat = 360
    public static let detailsTable: CGFloat = 242
    public static let fullTable: CGFloat = 320
    public static let events: CGFloat = 260
    public static let attention: CGFloat = 240
    public static let pairedEvents: CGFloat = 290
    public static let footer: CGFloat = 42
    public static let identityColumn: CGFloat = 260
    public static let directionColumn: CGFloat = 200
    public static let downloadColumn: CGFloat = 130
    public static let uploadColumn: CGFloat = 110
  }

  public enum Typography {
    public static func metric(size: CGFloat = 38, weight: NSFont.Weight = .regular) -> NSFont {
      .monospacedDigitSystemFont(ofSize: size, weight: weight)
    }
    public static func label(size: CGFloat = 14, weight: NSFont.Weight = .regular) -> NSFont {
      .systemFont(ofSize: size, weight: weight)
    }
    public static func title(_ size: CGFloat = 28) -> NSFont {
      .systemFont(ofSize: size, weight: .medium)
    }
    public static func caption() -> NSFont { .systemFont(ofSize: 12, weight: .regular) }
    public static var routeTitle: NSFont { .systemFont(ofSize: 20, weight: .medium) }
    public static var brand: NSFont { .systemFont(ofSize: 14, weight: .semibold) }
    public static var tableValue: NSFont {
      .monospacedDigitSystemFont(ofSize: 14, weight: .regular)
    }
    public static var time: NSFont { .monospacedDigitSystemFont(ofSize: 12, weight: .regular) }
    public static var section: NSFont { .systemFont(ofSize: 17, weight: .medium) }
    public static var secondaryMetric: NSFont {
      .monospacedDigitSystemFont(ofSize: 24, weight: .regular)
    }
    public static var status: NSFont { .systemFont(ofSize: 34, weight: .medium) }
  }

  public enum Symbol {
    public static func config(size: CGFloat = 16, weight: NSFont.Weight = .regular)
      -> NSImage.SymbolConfiguration
    {
      .init(pointSize: size, weight: weight)
    }
  }

  public enum Control {
    public static let height: CGFloat = 34
    public static let compactHeight: CGFloat = 24
    public static let horizontalPadding: CGFloat = 12
    public static let unit: CGFloat = 15
    public static let compactUnit: CGFloat = 13
    public static let selectedShadowOpacity: Float = 0.22
    public static let selectedShadowRadius: CGFloat = 6
    public static let navigationIcon: CGFloat = 16
    public static let navigationIconGap: CGFloat = 12
    public static let selectedShadowOffset = NSSize(width: 0, height: -2)
  }

  public enum Shadow {
    public static let cardOpacity: Float = 0.004
    public static let cardRadius: CGFloat = 3
    public static let cardOffset = NSSize(width: 0, height: -1)
  }

  /// 通用趋势组件的绘制尺寸与透明度。
  public enum Trend {
    public static let lineColor = Color.textSecondary
    public static let markerColor = Color.cardBorder
    public static let labelGap: CGFloat = 10
    public static let labelSize: CGFloat = 11
    public static let inset: CGFloat = 4
    public static let labelHeight: CGFloat = 18
    public static let lineWidth: CGFloat = 1.5
    public static let gridWidth: CGFloat = 0.5
    public static let gridOpacity: CGFloat = 0.6
    public static let fillOpacity: CGFloat = 0.10
    public static let pointRadius: CGFloat = 2.5
  }

  public enum RTT {
    public static let trendMetricWidth: CGFloat = 160
    public static let trendGap: CGFloat = 12
    public static let inset: CGFloat = 20
    public static let heading: CGFloat = 18
    public static let mainNumber: CGFloat = 58
    public static let compactNumber: CGFloat = 48
    public static let unit: CGFloat = 17
    public static let rowNumber: CGFloat = 13
    public static let small: CGFloat = 11
    public static let rowLabel: CGFloat = 13
    public static let badgePadding: CGFloat = 8
    public static let badgeHeight: CGFloat = 24
    public static let slotHeight: CGFloat = 22
    public static let mainStrip: CGFloat = 14
    public static let rowStrip: CGFloat = 20
    public static let historyPillWidth: CGFloat = 6
    public static let historyPillGap: CGFloat = 3.6
    public static let rowInset: CGFloat = 8
    public static let rowCardGap: CGFloat = 8
    public static let rowGap: CGFloat = 3
    public static let sectionGap: CGFloat = 12
    public static let minimumPickHeight: CGFloat = 26
    public static let rowRadius: CGFloat = 10
    public static let stripRadius: CGFloat = 5
    public static let compactWidth: CGFloat = 430
    public static let compactNumberHeight: CGFloat = 58
    public static let numberHeight: CGFloat = 68
    public static let deltaHeight: CGFloat = 26
    public static let tinyGap: CGFloat = 10
    public static let textLine: CGFloat = 20
    public static let badgeTextHeight: CGFloat = 15
    public static let badgeSmallRadius: CGFloat = 4
    public static let selectionInset: CGFloat = 2
    public enum Role { case text, bar }
    public static func endpoints(band: Int, role: Role) -> (NSColor, NSColor) {
      let tones = [DashboardColor.green, DashboardColor.amber, DashboardColor.red]
      let labels = [DashboardColor.greenLabel, DashboardColor.uploadLabel, DashboardColor.redLabel]
      let index = min(2, max(0, band))
      let base = role == .text ? labels[index] : tones[index]
      let end = role == .text ? base : base.blended(withFraction: 0.45, of: .white) ?? base
      return (base, end)
    }
  }

  /// 动效 token（motion.md §2）；场景映射与运行器见 `PaxMotion`。
  public enum Motion {
    public static let instant: TimeInterval = 0
    public static let fast: TimeInterval = 0.15
    public static let standard: TimeInterval = 0.25
    public static let enter = PaxCubicBezier(0.2, 0.7, 0.2, 1)
    public static let exit = PaxCubicBezier(0.4, 0, 1, 1)
  }
  /// 已确认工作台的尺寸；所有值按 AppKit point 表达，不按 Retina 像素放大。
  public enum Dashboard {
    public static let leftFraction: CGFloat = 1.15 / 2.15
    public static let pageHeader: CGFloat = 38
    public static let pageHeaderGap: CGFloat = 20
    /// 概览第 1 行（当前线路 | 今日流量）与第 2 行（模型用量趋势 | 模型排行）的统一行高。
    public static let primaryRowHeight: CGFloat = 330
    public static let modelRowHeight: CGFloat = 410
    public static let routeListHeight: CGFloat = 487
    public static let insightHeader: CGFloat = 44
    public static let insightRow: CGFloat = 56
    public static let insightRows = 3
    /// 卡片底栏（「流量明细 →」「查看日志 ›」、CLI 图例）高度。
    public static let cardFooter: CGFloat = 40
    /// 当前线路卡趋势：顶部峰值标注区、底部留白；模型卡左侧大数列宽。
    public static let trendLabelInset: CGFloat = 30
    public static let trendBottomInset: CGFloat = 12
    public static let modelHeadlineWidth: CGFloat = 190
    public static let modelHeadlineFraction: CGFloat = 0.31
    public static let modelHeadlineMinimum: CGFloat = 144
    public static let modelMinimumMetric: CGFloat = 26
    public static let modelChartOffset: CGFloat = 3
    public static let trafficLegendOffset: CGFloat = 2
    public static let badgeHeight: CGFloat = 24
    public static let actionButton: CGFloat = 96
    public static let actionCardHeight: CGFloat = 62
    public static let inset: CGFloat = 16
    public static let nodeInset: CGFloat = 18
    public static let icon: CGFloat = 30
    public static let iconGlyph: CGFloat = 17
    public static let iconRadius: CGFloat = 9
    public static let titleGap: CGFloat = 9
    public static let headerHeight: CGFloat = 61
    public static let tableHeader: CGFloat = 39
    public static let rowHeight: CGFloat = 53
    public static let minimumRows = 5
    public static let maximumRows = 10
    public static let routeSlots = 6
    public static let routeRow: CGFloat = 58
    public static let routeGap: CGFloat = 2
    public static let routeHeader: CGFloat = 60
    public static let routeListInset: CGFloat = 17
    public static let routeNameFont: CGFloat = 15
    public static let routeMetaFont: CGFloat = 14
    public static let routeColumnGap: CGFloat = 9
    public static let routeContentInset: CGFloat = 5
    public static let routeLineGap: CGFloat = 5
    public static let routeHistoryHeight: CGFloat = 18
    public static let routeProtocolFont: CGFloat = 12
    public static let metricY: CGFloat = 94
    public static let metricWidth: CGFloat = 138
    public static let largeMetric: CGFloat = 44
    public static let secondaryMetric: CGFloat = 27
    public static let bodyTitle: CGFloat = 14
    public static let nodeTitle: CGFloat = 16
    public static let bodyFont: CGFloat = 12
    public static let captionFont: CGFloat = 11
    public static let eventRow: CGFloat = 59
    public static let cellInset: CGFloat = 8
    public static let requestPeakWidth: CGFloat = 72
    public static let requestHeight: CGFloat = 32
    public static let requestGap: CGFloat = 7
    public static let transferHeight: CGFloat = 7
    public static let transferContentHeight: CGFloat = 26.5
    public static let transferGlyphRasterOffset: CGFloat = 0.5
    public static let transferNumberInkGap: CGFloat = 7
    public static let transferArrowInkGap: CGFloat = 5
    public static let objectShadowOpacity: Float = 0.12
    public static let objectShadowRadius: CGFloat = 1
    public static let objectShadowOffset = NSSize(width: 0, height: -0.5)
    public static let transferGap: CGFloat = 3
    public static let tableBadgePadding: CGFloat = 5
    public static let tableBadgeHeight: CGFloat = 23
    public static let tableLineGap: CGFloat = 10
    public static let navGap: CGFloat = 10
    public static let pageTitle: CGFloat = 26
    public static let tabShadowOpacity: CGFloat = 0.09
    public static let tabHeight: CGFloat = 32
    public static let tabPadding: CGFloat = 2
    public static let tabRadius: CGFloat = 9
    public static let modeSize = NSSize(width: 136.4, height: 35.2)
    public static let modePadding: CGFloat = 3.3
    public static let dragThreshold: CGFloat = 4
  }

  public enum DashboardColor {
    private static func color(_ name: String, _ hex: UInt32) -> NSColor {
      Color.adaptive(name, light: hex, dark: hex)
    }
    public static let icon = Color.textSecondary
    public static let iconBorder = color("IconBorder", 0xe0e3e7)
    public static let divider = color("RowDivider", 0xe8e8eb)
    public static let tableEdge = color("TableEdge", 0xdfe2e6)
    public static let rowHover = color("RouteHover", 0xf6f6f7)
    // 三个彩色系列；语义 token 引用此处，开关材质单独保留原稿。
    public static let green = color("Emerald", 0x10b981)
    public static let amber = color("Amber", 0xf5b529)
    public static let red = color("Coral", 0xef5261)
    public static let greenLabel = color("EmeraldLabel", 0x087f5b)
    public static let uploadLabel = color("AmberLabel", 0x966900)
    public static let redLabel = color("CoralLabel", 0xbc3445)
    public static let rtt = [green, amber, red]
    private static func tint(_ color: NSColor) -> NSColor {
      color.blended(withFraction: 0.38, of: .white) ?? color
    }
    public static let download = [green, tint(green), green]
    public static let upload = [amber, tint(amber), amber]
    public static let transferUpload = [tint(amber), amber]
    public static let transferDownload = [tint(green), green]
    public static let track = [color("TrackStart", 0xdddfe3), color("TrackEnd", 0xebecef)]
    public static let proxyFill = Color.adaptive("PaxProxyTag", light: 0xf0f5fd, dark: 0x273449)
    public static let proxyText = Color.textSecondary
    public static let directFill = Color.adaptive("PaxDirectTag", light: 0xeff7f2, dark: 0x283a30)
    public static let directText = Color.textSecondary
    public static let modeProxy = [
      color("ModeProxyTop", 0xcbf6e8), color("ModeProxyMid", 0x92d9c3),
      color("ModeProxyBottom", 0xb7f0dc),
    ]
    public static let modeDirect = [
      color("ModeDirectTop", 0xe8edef), color("ModeDirectMid", 0xbfc6cb),
      color("ModeDirectBottom", 0xdde2e5),
    ]
    public static let modeProxyEdge = color("ModeProxyEdge", 0x8bc9b6)
    public static let modeDirectEdge = color("ModeDirectEdge", 0xb9c0c6)
    public static let modeProxyText = color("ModeProxyText", 0x154b39)
    public static let modeProxyInactiveText = color("ModeProxyInactiveText", 0x326657)
    public static let modeDirectInactiveText = color("ModeDirectInactiveText", 0x606872)
    public static let modeTrackShadow = color("ModeTrackShadow", 0x386c57)
    public static let modeDirectTrackShadow = color("ModeDirectTrackShadow", 0x394552)
    public static let modeTrackShade = color("ModeTrackShade", 0x39745f).withAlphaComponent(0.15)
    public static let modeDirectTrackShade = color("ModeDirectTrackShade", 0x606b77)
      .withAlphaComponent(0.15)
    public static let modeThumbShade = color("ModeThumbShade", 0xa4beb5).withAlphaComponent(0.20)
    public static let modeThumbShadow = color("ModeThumbShadow", 0x385b4d)
    public static let modeProxyThumb = [
      color("ModeThumbTop", 0xffffff), color("ModeThumbMiddle", 0xf6fffb),
      color("ModeThumbBottom", 0xe7f4ed),
    ]
    public static let modeDirectThumb = [
      color("ModeDirectThumbTop", 0xffffff), color("ModeDirectThumbMiddle", 0xf6f8fa),
      color("ModeDirectThumbBottom", 0xe8edf0),
    ]
    public static let modeDirectText = color("ModeDirectText", 0x343c46)
    public static let warningFill = amber.withAlphaComponent(0.10)
    public static let warningText = uploadLabel
    public static let successFill = green.withAlphaComponent(0.10)
    public static let successText = greenLabel
    public static let neutralFill = Color.navigationHover
  }

  public enum Surface {
    public static let cardTopHighlight: CGFloat = 0.93
    public static let cardSideHighlight: CGFloat = 0.45
    public static let highlightBlur: CGFloat = 1
    public static let highlightExtent: CGFloat = 8
    public static let highlightLayer: CGFloat = 100
    public static let bottomOffset = NSSize(width: 0, height: 1)
    public static let topOffset = NSSize(width: 0, height: -1)
    public static let leftOffset = NSSize(width: 1, height: 0)
    public static let rightOffset = NSSize(width: -1, height: 0)
  }
  /// 分段页签：凹槽底面与凸起滑块共享的光影。
  public enum Tabs {
    public static let horizontalInset: CGFloat = 12
    public static let itemSpacing: CGFloat = 4
    public static let iconGap: CGFloat = 6
    public static let labelFont: CGFloat = 13
    public static let countFont: CGFloat = 12
    public static let countColor = Color.adaptive("PaxTabCount", light: 0x96999e, dark: 0xb2b3ba)
    public static let trackFill = Color.adaptive("PaxTabTrack", light: 0xe5e5e8, dark: 0x24252a)
    public static let thumbBottom = Color.adaptive(
      "PaxTabThumbBottom", light: 0xf4f5f7, dark: 0x303238)
    public static let trackTopShadow = Color.shadow.withAlphaComponent(0.12)
    public static let trackBottomBorder = NSColor.white.withAlphaComponent(0.95)
    public static let trackInnerBlur: CGFloat = 2
    public static let thumbTopHighlight: CGFloat = 0.95
    public static let thumbSideHighlight: CGFloat = 0.28
    public static let thumbBottomShade = Color.shadow.withAlphaComponent(0.06)
    public static let thumbInnerBlur: CGFloat = 1
    public static let gradientAngle: CGFloat = 90
  }
  public enum Mode {
    public static let middleStop: CGFloat = 0.48
    public static let thumbMiddleStop: CGFloat = 0.58
    public static let verticalAngle: CGFloat = 90
    public static let trackHighlight: CGFloat = 0.85
    public static let thumbHighlight: CGFloat = 1
    public static let innerShadowBlur: CGFloat = 2
    public static let trackShadowOpacity: CGFloat = 0.094
    public static let thumbShadowOpacity: CGFloat = 0.21
    public static let shadowBlur: CGFloat = 4
    public static let shadowOffset = NSSize(width: 0, height: -2)
    public static func labelFont(selected: Bool) -> NSFont {
      NSFont(
        name: selected ? "PingFangSC-Semibold" : "PingFangSC-Medium", size: Dashboard.bodyFont * 1.1
      )
        ?? Typography.label(size: Dashboard.bodyFont * 1.1, weight: selected ? .bold : .medium)
    }
  }

  public enum MiniChart {
    public static let trafficLeft: CGFloat = 52
    public static let trafficRight: CGFloat = 8
    public static let trafficTop: CGFloat = 16
    public static let trafficBottom: CGFloat = 121
    public static let trafficPlotHeight: CGFloat = 105
    public static let barWidth: CGFloat = 10
    public static let barFraction: CGFloat = 0.48
    public static let barRadius: CGFloat = 3
    public static let trafficCeiling: Double = 300
    public static let trafficTick: Double = 150
    public static let trafficAxisY: CGFloat = 129
    public static let nodeBottom: CGFloat = 0.90
    public static let nodeHeight: CGFloat = 0.53
    public static let nodeGrid: [CGFloat] = [0.56, 0.77]
    public static let compactBottom: CGFloat = 0.90
    public static let compactHeight: CGFloat = 0.80
    public static let areaOpacity: CGFloat = 0.07
  }

}
