import AppKit

extension PaxStackedChart {
  /// Darker at the top, lighter at the bottom (or solid with 45° stripes when `hatched`); `rounded` gives the top corners a 4pt radius.
  static func drawShaded(
    _ rect: NSRect, color: NSColor, rounded: Bool, hatched: Bool = false
  ) {
    guard rect.height > 0 else { return }
    let radius = rounded ? min(PaxTheme.Chart.barTopRadius, rect.width / 2, rect.height) : 0
    let path = NSBezierPath()
    // The view is flipped, so minY is the visual top.
    path.move(to: NSPoint(x: rect.minX, y: rect.maxY))
    path.line(to: NSPoint(x: rect.minX, y: rect.minY + radius))
    if radius > 0 {
      path.appendArc(
        withCenter: NSPoint(x: rect.minX + radius, y: rect.minY + radius), radius: radius,
        startAngle: 180, endAngle: 270)
      path.line(to: NSPoint(x: rect.maxX - radius, y: rect.minY))
      path.appendArc(
        withCenter: NSPoint(x: rect.maxX - radius, y: rect.minY + radius), radius: radius,
        startAngle: 270, endAngle: 360)
    } else {
      path.line(to: NSPoint(x: rect.maxX, y: rect.minY))
    }
    path.line(to: NSPoint(x: rect.maxX, y: rect.maxY))
    path.close()
    if hatched {
      color.setFill()
      path.fill()
      NSGraphicsContext.saveGraphicsState()
      path.addClip()
      PaxShareBar.stripes(in: rect)
      NSGraphicsContext.restoreGraphicsState()
      return
    }
    let dark = color.shadow(withLevel: PaxTheme.Chart.barShade) ?? color
    let light = color.highlight(withLevel: PaxTheme.Chart.barShade) ?? color
    NSGradient(starting: dark, ending: light)?.draw(in: path, angle: 90)
  }
}
