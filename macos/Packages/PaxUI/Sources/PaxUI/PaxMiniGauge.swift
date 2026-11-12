import AppKit

/// 半圆仪表：低透明度轨道上从左起按比例填充（浅到深渐变），端点一个白色旋钮，百分比文字居中在弧内。
public final class PaxMiniGauge: NSView {
  public static let size = NSSize(width: 72, height: 40)
  private static let line: CGFloat = 6
  private let fraction: Double?
  private let color: NSColor
  private let text: String

  /// `fraction` in 0...1; nil draws only the track (no data).
  public init(fraction: Double?, text: String, color: NSColor) {
    self.fraction = fraction.map { min(1, max(0, $0)) }
    self.text = text
    self.color = color
    super.init(frame: .zero)
    setAccessibilityElement(true)
    setAccessibilityRole(.image)
    setAccessibilityLabel(text)
    toolTip = text
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var intrinsicContentSize: NSSize { Self.size }
  public override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }

  /// 沿弧线（x 单调）从浅到深填充：浅端取主色提亮，深端取主色压暗；颜色在绘制时解析，随外观刷新。
  private func fillGradient(_ path: NSBezierPath, from minX: CGFloat, to maxX: CGFloat) {
    guard let context = NSGraphicsContext.current?.cgContext else { return }
    let light = color.blended(withFraction: 0.3, of: .white) ?? color
    let deep = color.blended(withFraction: 0.22, of: .black) ?? color
    context.saveGState()
    context.addPath(
      path.cgPath.copy(
        strokingWithWidth: path.lineWidth, lineCap: .round, lineJoin: .round, miterLimit: 10))
    context.clip()
    NSGradient(starting: light, ending: deep)?.draw(
      in: NSRect(x: minX, y: 0, width: max(1, maxX - minX), height: bounds.height), angle: 0)
    context.restoreGState()
  }

  public override func draw(_ dirtyRect: NSRect) {
    let width = Self.line
    let radius = (bounds.width - width) / 2
    let center = NSPoint(x: bounds.midX, y: width / 2 + 1)
    func arc(to end: CGFloat) -> NSBezierPath {
      let path = NSBezierPath()
      path.appendArc(
        withCenter: center, radius: radius, startAngle: 180, endAngle: end, clockwise: true)
      path.lineWidth = width
      path.lineCapStyle = .round
      return path
    }
    color.withAlphaComponent(0.2).setStroke()
    arc(to: 0).stroke()
    if let fraction, fraction > 0 {
      let angle = CGFloat(180 - 180 * fraction)
      let radians = angle * .pi / 180
      let knob = NSPoint(
        x: center.x + radius * cos(radians), y: center.y + radius * sin(radians))
      fillGradient(arc(to: angle), from: center.x - radius - width / 2, to: knob.x + width / 2)
      let r = width / 2 - 1
      NSColor.white.setFill()
      NSBezierPath(ovalIn: NSRect(x: knob.x - r, y: knob.y - r, width: r * 2, height: r * 2)).fill()
    }
    let attributes: [NSAttributedString.Key: Any] = [
      .font: PaxTheme.Typography.time, .foregroundColor: PaxTheme.Color.text,
    ]
    let size = (text as NSString).size(withAttributes: attributes)
    let origin = NSPoint(x: bounds.midX - size.width / 2, y: center.y + width / 2)
    (text as NSString).draw(at: origin, withAttributes: attributes)
  }
}
