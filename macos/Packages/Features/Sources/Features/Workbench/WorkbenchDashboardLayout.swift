import AppKit
import PaxUI

/// 概览纵向版式：页头、可选行动卡、双列线路与流量、通栏模型用量与流量分析、最近变化。
/// 净内容宽不足 700pt 时全部纵向堆叠。
final class WorkbenchGrid: NSView {
  var notice = NSView()
  var header = NSView(), action = NSView(), route = NSView(), traffic = NSView(),
    usage = NSView(), analysis = NSView(),
    changes = NSView()
  /// 行动卡是否占位；正常时不占位。
  var showsAction = false
  override var isFlipped: Bool { true }
  func install() {
    for v in [
      header, notice, action, route, traffic, usage, analysis, changes,
    ] {
      addSubview(v)
    }
    needsLayout = true
  }
  override func layout() {
    super.layout()
    guard let scroll = enclosingScrollView else { return }
    let d = PaxTheme.Dashboard.self
    let margin = PaxTheme.Spacing.page
    let gap = PaxTheme.Spacing.cardGap
    let width = scroll.contentSize.width
    let inner = max(1, width - margin * 2)
    var y = PaxTheme.Spacing.pageTop
    header.frame = NSRect(x: margin, y: y, width: inner, height: d.pageHeader)
    y += d.pageHeader + d.pageHeaderGap
    if !notice.isHidden {
      let height = notice.fittingSize.height
      notice.frame = NSRect(x: margin, y: y, width: inner, height: height)
      y += height + PaxTheme.Spacing.tight
    }
    action.isHidden = !showsAction
    if showsAction {
      action.frame = NSRect(x: margin, y: y, width: inner, height: d.actionCardHeight)
      y += d.actionCardHeight + gap
    }
    let wide = inner >= PaxTheme.Layout.wideContent
    let left = (inner - gap) * d.leftFraction
    let right = inner - left - gap
    let firstHeight = d.primaryRowHeight
    if wide {
      route.frame = NSRect(x: margin, y: y, width: left, height: firstHeight)
      traffic.frame = NSRect(x: margin + left + gap, y: y, width: right, height: firstHeight)
      y += firstHeight + gap
    } else {
      route.frame = NSRect(x: margin, y: y, width: inner, height: firstHeight)
      y += firstHeight + gap
      traffic.frame = NSRect(x: margin, y: y, width: inner, height: firstHeight)
      y += firstHeight + gap
    }
    let usageHeight: CGFloat = wide ? min(350, max(284, inner * 0.295)) : 510
    usage.frame = NSRect(x: margin, y: y, width: inner, height: usageHeight)
    y += usageHeight + gap
    let analysisHeight = (analysis as? PaxTrafficAnalysisCard)?.preferredHeight ?? d.headerHeight
    analysis.frame = NSRect(x: margin, y: y, width: inner, height: analysisHeight)
    y += analysisHeight + gap
    let changesHeight: CGFloat = 34 * 1.3
    changes.frame = NSRect(x: margin, y: y, width: inner, height: changesHeight)
    y += changesHeight + gap
    let size = NSSize(width: width, height: y + margin)
    if frame.size != size { setFrameSize(size) }
  }
}

/// 系统内容背景；数据卡片不透出壁纸。
final class WorkbenchSurface: NSView {
  var surfaceColor: NSColor = PaxTheme.Color.workspace { didSet { needsDisplay = true } }
  override var wantsUpdateLayer: Bool { true }
  override init(frame frameRect: NSRect) {
    super.init(frame: frameRect)
    wantsLayer = true
  }
  convenience init() { self.init(frame: .zero) }
  @available(*, unavailable)
  required init?(coder: NSCoder) { fatalError("代码构建") }
  override func updateLayer() {
    effectiveAppearance.performAsCurrentDrawingAppearance {
      layer?.backgroundColor = surfaceColor.cgColor
    }
  }
}

final class WorkbenchScrollView: NSScrollView {
  var onReading: (() -> Void)?
  private var documentWidth: CGFloat?
  override func scrollWheel(with event: NSEvent) {
    if event.scrollingDeltaY != 0 { onReading?() }
    super.scrollWheel(with: event)
  }
  override func tile() {
    super.tile()
    // Scrolling changes the clip origin, not the card geometry. Content changes
    // already request layout at their owning dashboard update sites.
    guard documentWidth != contentSize.width else { return }
    documentWidth = contentSize.width
    documentView?.needsLayout = true
  }
}

/// AppKit 滚动文档使用从上到下的坐标，首屏不会默认落在长内容底部。
final class WorkbenchDocument: NSView {
  override var isFlipped: Bool { true }

}
