import AppKit

/// 内高光/阴影绘制：裁切到内容内部，把外部反向路径的柔和阴影投向内侧。
@MainActor
enum PaxSurfaceHighlight {
  static func draw(
    in rect: NSRect, radius: CGFloat, topOpacity: CGFloat, sideOpacity: CGFloat,
    bottomShade: NSColor? = nil, blur: CGFloat = PaxTheme.Surface.highlightBlur,
    topShade: NSColor? = nil
  ) {
    guard rect.width > 0, rect.height > 0 else { return }
    NSGraphicsContext.saveGraphicsState()
    let clip = NSBezierPath(roundedRect: rect, xRadius: radius, yRadius: radius)
    clip.addClip()
    let inverse = NSBezierPath(
      rect: rect.insetBy(
        dx: -PaxTheme.Surface.highlightExtent, dy: -PaxTheme.Surface.highlightExtent))
    inverse.append(clip)
    inverse.windingRule = .evenOdd
    var edges: [(NSSize, NSColor)] = [
      (PaxTheme.Surface.topOffset, topShade ?? NSColor.white.withAlphaComponent(topOpacity)),
      (PaxTheme.Surface.leftOffset, NSColor.white.withAlphaComponent(sideOpacity)),
      (PaxTheme.Surface.rightOffset, NSColor.white.withAlphaComponent(sideOpacity)),
    ]
    if let bottomShade { edges.append((PaxTheme.Surface.bottomOffset, bottomShade)) }
    for (offset, color) in edges {
      NSGraphicsContext.saveGraphicsState()
      let shadow = NSShadow()
      shadow.shadowColor = color
      shadow.shadowBlurRadius = blur
      shadow.shadowOffset = offset
      shadow.set()
      NSColor.white.setFill()
      inverse.fill()
      NSGraphicsContext.restoreGraphicsState()
    }
    NSGraphicsContext.restoreGraphicsState()
  }
}

/// 绘制层在所有卡片内容之上；不挡点击、焦点或辅助访问。
final class PaxInnerHighlight: NSView {
  private var cachedImage: CGImage?
  private var cachedSize = NSSize.zero
  private var cachedScale: CGFloat = 0
  override var isFlipped: Bool { true }
  override var wantsUpdateLayer: Bool { true }
  override init(frame frameRect: NSRect) {
    super.init(frame: frameRect)
    wantsLayer = true
    layerContentsRedrawPolicy = .onSetNeedsDisplay
    layer?.zPosition = PaxTheme.Surface.highlightLayer
    setAccessibilityElement(false)
  }
  convenience init() { self.init(frame: .zero) }
  @available(*, unavailable)
  required init?(coder: NSCoder) { fatalError("代码构建") }
  override func hitTest(_ point: NSPoint) -> NSView? { nil }
  override func setFrameSize(_ newSize: NSSize) {
    let changed = frame.size != newSize
    super.setFrameSize(newSize)
    if changed { invalidateImage() }
  }
  override func viewDidChangeBackingProperties() {
    super.viewDidChangeBackingProperties()
    invalidateImage()
  }
  override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    invalidateImage()
  }
  private func invalidateImage() {
    cachedImage = nil
    needsDisplay = true
  }
  override func updateLayer() {
    let scale = window?.backingScaleFactor ?? 1
    if cachedImage == nil || cachedSize != bounds.size || cachedScale != scale {
      cachedImage = makeImage(scale: scale)
      cachedSize = bounds.size
      cachedScale = scale
    }
    // Hand the bitmap to Core Animation directly. Drawing an NSImage here would
    // replay image conversion/blending whenever AppKit redisplays the backing layer.
    layer?.contentsScale = scale
    layer?.contents = cachedImage
  }
  // One bitmap per card; resizing replaces it instead of accumulating size variants.
  private func makeImage(scale: CGFloat) -> CGImage? {
    guard bounds.width > 0, bounds.height > 0,
      let bitmap = NSBitmapImageRep(
        bitmapDataPlanes: nil, pixelsWide: Int(ceil(bounds.width * scale)),
        pixelsHigh: Int(ceil(bounds.height * scale)), bitsPerSample: 8,
        samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB,
        bytesPerRow: 0, bitsPerPixel: 0),
      let context = NSGraphicsContext(bitmapImageRep: bitmap)
    else { return nil }
    bitmap.size = bounds.size
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(cgContext: context.cgContext, flipped: true)
    context.cgContext.clear(CGRect(x: 0, y: 0, width: bitmap.pixelsWide, height: bitmap.pixelsHigh))
    context.cgContext.translateBy(x: 0, y: CGFloat(bitmap.pixelsHigh))
    context.cgContext.scaleBy(x: scale, y: -scale)
    drawHighlight()
    NSGraphicsContext.restoreGraphicsState()
    return bitmap.cgImage
  }
  private func drawHighlight() {
    let inset = PaxTheme.Layout.cardBorder
    PaxSurfaceHighlight.draw(
      in: bounds.insetBy(dx: inset, dy: inset), radius: PaxTheme.Radius.card - inset,
      topOpacity: PaxTheme.Surface.cardTopHighlight, sideOpacity: PaxTheme.Surface.cardSideHighlight
    )
  }
}
