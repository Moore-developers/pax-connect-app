import AppKit

/// 云团品牌标记：关闭使用系统模板，运行浅蓝，异常红色并附提示点。
@MainActor
public enum PaxMenuBarIcon {
  public static func image(isActive: Bool, needsAttention: Bool = false) -> NSImage {
    let image = NSImage(size: NSSize(width: 18, height: 18), flipped: true) { _ in
      let cloud = NSBezierPath()
      cloud.move(to: NSPoint(x: 4, y: 7))
      cloud.curve(
        to: NSPoint(x: 8, y: 3), controlPoint1: NSPoint(x: 4.5, y: 4),
        controlPoint2: NSPoint(x: 6, y: 3))
      cloud.curve(
        to: NSPoint(x: 12.5, y: 6.5), controlPoint1: NSPoint(x: 11, y: 3),
        controlPoint2: NSPoint(x: 11.5, y: 5))
      cloud.curve(
        to: NSPoint(x: 16.5, y: 11), controlPoint1: NSPoint(x: 15, y: 6.5),
        controlPoint2: NSPoint(x: 16.5, y: 8.5))
      cloud.curve(
        to: NSPoint(x: 12, y: 15), controlPoint1: NSPoint(x: 16.5, y: 14),
        controlPoint2: NSPoint(x: 14, y: 15))
      cloud.curve(
        to: NSPoint(x: 8.5, y: 16), controlPoint1: NSPoint(x: 10.5, y: 15),
        controlPoint2: NSPoint(x: 10.5, y: 16))
      cloud.curve(
        to: NSPoint(x: 5.5, y: 15), controlPoint1: NSPoint(x: 7, y: 16),
        controlPoint2: NSPoint(x: 7, y: 15))
      cloud.curve(
        to: NSPoint(x: 1.5, y: 11), controlPoint1: NSPoint(x: 2.5, y: 15),
        controlPoint2: NSPoint(x: 1.5, y: 13.5))
      cloud.curve(
        to: NSPoint(x: 4, y: 7), controlPoint1: NSPoint(x: 1.5, y: 9),
        controlPoint2: NSPoint(x: 2.5, y: 7.5))
      cloud.close()
      let eyes = NSBezierPath()
      for x: CGFloat in [6.25, 10.25] {
        eyes.appendOval(in: NSRect(x: x, y: 10, width: 1.5, height: 2))
      }
      let color: NSColor =
        needsAttention
        ? NSColor(srgbRed: 0.9, green: 0.22, blue: 0.25, alpha: 1)
        : isActive
          ? NSColor(srgbRed: 0.43, green: 0.68, blue: 1, alpha: 1)
          : .black
      color.set()
      if isActive {
        cloud.windingRule = .evenOdd
        cloud.append(eyes)
        cloud.fill()
      } else {
        cloud.lineWidth = 1.25
        cloud.lineJoinStyle = .round
        cloud.stroke()
        eyes.fill()
      }
      if needsAttention {
        // 留出透明隔离圈，深浅色菜单栏均可辨识提示点。
        if let context = NSGraphicsContext.current?.cgContext {
          context.saveGState()
          context.setBlendMode(.clear)
          context.fillEllipse(in: CGRect(x: 12.5, y: 0.5, width: 5, height: 5))
          context.restoreGState()
        }
        NSBezierPath(ovalIn: NSRect(x: 13.5, y: 1.5, width: 3, height: 3)).fill()
      }
      return true
    }
    image.isTemplate = !isActive && !needsAttention
    return image
  }
}
