import AppKit

extension PaxTheme {
  /// 最终线路原稿的渲染值；CSS px 对应 AppKit point，不能按 Retina 再放大。
  public enum Routes {
    private static func color(_ hex: UInt32) -> NSColor {
      NSColor(
        srgbRed: CGFloat((hex >> 16) & 255) / 255, green: CGFloat((hex >> 8) & 255) / 255,
        blue: CGFloat(hex & 255) / 255, alpha: 1)
    }
    public static let canvas = color(0xf5f6f8)
    public static let sidebar = color(0xeff1f5)
    public static let card = color(0xffffff)
    public static let text = color(0x242528)
    public static let muted = color(0x7c838d)
    public static let edge = color(0xe5e5e7)
    public static let line = color(0xeef0f3)
    public static let blue = color(0x2587ee)
    public static let selectedBlue = color(0x1678db)
    public static let purple = color(0x8b5cf6)
    public static let selectionFill = blue.blended(withFraction: 0.97, of: card) ?? card
    public static let tabFill = blue.blended(withFraction: 0.90, of: card) ?? card
    public static let tagFill = blue.blended(withFraction: 0.91, of: card) ?? card
    public static let menuFill = tagFill
    public static let backdrop = color(0x25334c).withAlphaComponent(0.19)
    public static let shadow = color(0x142843).withAlphaComponent(0.18)
    public static let pageInset: CGFloat = 22
    public static let pageTop: CGFloat = 20
    public static let rowHeight: CGFloat = 64
    public static let headerHeight: CGFloat = 44
    public static let ordinalWidth: CGFloat = 40
    public static let featureWidth: CGFloat = 112
    public static let valueWidth: CGFloat = 128
    public static let nameMinimum: CGFloat = 182
    public static let tableMinimum: CGFloat = 718
    public static let columnShares: [CGFloat] = [0, 0.30, 0.10, 0.20, 0.20, 0.20]
    public static let cellInset: CGFloat = 12
    public static let railWidth: CGFloat = 3
    public static let bodyFont: CGFloat = 13
    public static let failureFont: CGFloat = 18
    public static let nameFont: CGFloat = 14
    public static let metaFont: CGFloat = 12
    public static let controlHeight: CGFloat = 33
    public static let controlInset: CGFloat = 10
    public static let controlRadius: CGFloat = 10
    public static let searchWidth: CGFloat = 300
    public static let toolsGap: CGFloat = 10
    public static let groupGap: CGFloat = 14
    public static let tableRadius: CGFloat = 18
    public static let tagHeight: CGFloat = 20
    public static let currentHeight: CGFloat = 23
    public static let tagRadius: CGFloat = 10
    public static let tagInset: CGFloat = 7
    public static let currentGradient = [color(0x2388ee), color(0x0870d5)]
    public static let formLabelWidth: CGFloat = 90
    public static let tabHeight: CGFloat = 33
    public static let detailTabHeight: CGFloat = 43
    public static let detailTabIcon: CGFloat = 16
    public static let detailTabIconGap: CGFloat = 6
    public static let detailTabUnderline: CGFloat = 2
    public static let detailTabGap: CGFloat = 8
    public static let detailTabInset: CGFloat = 12
    public static let detailTabTop: CGFloat = 4
    public static let detailTabIndicatorGap: CGFloat = 6
    public static let detailTabRadius: CGFloat = 8
    public static let detailTabFont: CGFloat = 13
    public static let detailTabSelectedFill = canvas
    public static let detailTitleFont: CGFloat = 19
    public static let dialog = NSSize(width: 830, height: 794)
    public static let auxiliaryTitleFont: CGFloat = 17
    public static let auxiliaryInset: CGFloat = 24
    public static let legendSize: CGFloat = 6
    public static let picker = NSSize(width: 716, height: 540)
    public static let cfScanWidth: CGFloat = 194
    public static let diagnosticLabelWidth: CGFloat = 190
    public static let diagnosticWorkspace = NSSize(width: 940, height: 680)
    public static let measurementColumn: CGFloat = 148
    public static let dialogInset: CGFloat = 26
    public static let bodyInset: CGFloat = 22
    public static let bodyTop: CGFloat = 18
    public static let dialogRadius: CGFloat = 18
    public static let overlayInset: CGFloat = 24
    public static let detailMaximumWidth: CGFloat = 1040
    public static let auxiliaryMaximumWidth: CGFloat = 900
    public static let dialogWindowFraction: CGFloat = 0.82
    public static let headerIcon: CGFloat = 44
    public static let pickerIcon: CGFloat = 40
    public static let metricFont: CGFloat = 28
    public static let unitFont: CGFloat = 12
    public static let usageFont: CGFloat = 23
    public static let metricLine: CGFloat = 60
    public static let metricGap: CGFloat = 8
    public static let miniHeight: CGFloat = 48
    public static let miniWidth: CGFloat = 94
    public static let chartLeft: CGFloat = 56
    public static let chartTop: CGFloat = 17
    public static let chartAxis: CGFloat = 29
    public static let candidateRowHeight: CGFloat = 44
    public static let choiceWidth: CGFloat = 30
    public static let candidateIdentityFraction: CGFloat = 0.43
    public static let chartGapDash: [CGFloat] = [4, 3]
    public static let chartGapOpacity: CGFloat = 0.35
    public static let chartPointRadius: CGFloat = 2
    public static let chartLineWidth: CGFloat = 1.8
    public static let chartAreaOpacity: CGFloat = 0.22
    public static let chartScaleHeadroom: Double = 1.1
    public static let chartHeight: CGFloat = 160
    public static let sectionRadius: CGFloat = 17
    public static let sectionInset: CGFloat = 20
    public static let sectionTop: CGFloat = 16
    public static let sectionGap: CGFloat = 12
    public static let pathIcon: CGFloat = 38
    public static let pathRadius: CGFloat = 12
    public static let pathTopHighlight: CGFloat = 0.95
    public static let pathSideHighlight: CGFloat = 0.28
    public static let pathHighlightBlur: CGFloat = 1
    public static let fieldHeight: CGFloat = 38
    public static let fieldGap: CGFloat = 18
    public static let fieldColumnGap: CGFloat = 26
    public static let footerInset: CGFloat = 14
    public static let tagOpacity: CGFloat = 0.09
    public static let tagShadowOpacity: CGFloat = 0.08
    public static let tagShadowBlur: CGFloat = 2
    public static let testDuration: Duration = .milliseconds(550)
  }
}
