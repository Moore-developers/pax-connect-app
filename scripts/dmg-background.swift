import AppKit

// Finder icon positions and this background share a 760 × 520 point canvas.
let width: CGFloat = 760
let height: CGFloat = 520
let image = NSImage(size: NSSize(width: width, height: height))
image.lockFocus()
NSColor(calibratedRed: 0.96, green: 0.97, blue: 0.99, alpha: 1).setFill()
NSRect(x: 0, y: 0, width: width, height: height).fill()

func label(
  _ value: String, top: CGFloat, x: CGFloat = 0, width: CGFloat = 760,
  size: CGFloat, weight: NSFont.Weight = .regular, color: NSColor = .labelColor
) {
  let paragraph = NSMutableParagraphStyle()
  paragraph.alignment = .center
  (value as NSString).draw(
    in: NSRect(x: x, y: height - top - 40, width: width, height: 40),
    withAttributes: [
      .font: NSFont.systemFont(ofSize: size, weight: weight),
      .foregroundColor: color, .paragraphStyle: paragraph,
    ])
}
// Use explicit colors: Finder can show a dark appearance over a light image.
let ink = NSColor(calibratedRed: 0.14, green: 0.18, blue: 0.25, alpha: 1)
let muted = NSColor(calibratedRed: 0.40, green: 0.45, blue: 0.53, alpha: 1)
label("安装 Pax Connect", top: 32, size: 30, weight: .semibold, color: ink)
label("拖入应用程序，然后从应用程序打开", top: 80, size: 16, color: muted)
label("① 拖入右侧文件夹", top: 282, x: 75, width: 260, size: 16, weight: .medium, color: ink)
label("② 安装后从这里打开", top: 282, x: 425, width: 260, size: 16, weight: .medium, color: ink)
let arrow = NSBezierPath()
arrow.move(to: NSPoint(x: 330, y: height - 200))
arrow.line(to: NSPoint(x: 430, y: height - 200))
arrow.move(to: NSPoint(x: 415, y: height - 185))
arrow.line(to: NSPoint(x: 430, y: height - 200))
arrow.line(to: NSPoint(x: 415, y: height - 215))
arrow.lineWidth = 4
arrow.lineCapStyle = .round
arrow.lineJoinStyle = .round
NSColor(calibratedRed: 0.39, green: 0.46, blue: 0.71, alpha: 1).setStroke()
arrow.stroke()
let separator = NSBezierPath()
separator.move(to: NSPoint(x: 70, y: height - 337))
separator.line(to: NSPoint(x: 690, y: height - 337))
separator.lineWidth = 1
NSColor(calibratedWhite: 0.86, alpha: 1).setStroke()
separator.stroke()
label("首次打开遇到系统提示？请查看安装帮助", top: 486, size: 13, color: muted)
image.unlockFocus()
guard let tiff = image.tiffRepresentation, let bitmap = NSBitmapImageRep(data: tiff),
  let png = bitmap.representation(using: .png, properties: [:])
else {
  fatalError("Unable to render DMG background")
}
try png.write(to: URL(fileURLWithPath: CommandLine.arguments[1]))
