import AppKit

/// Image canvas and visible artwork are different sizes. This local raster pass
/// discards transparent padding once per assigned image, preserving aspect ratio.
@MainActor enum PaxImageInk {
  private final class Cached {
    let pixels: Int
    let image: NSImage
    init(pixels: Int, image: NSImage) {
      self.pixels = pixels
      self.image = image
    }
  }
  private static let cache = NSMapTable<NSImage, Cached>(
    keyOptions: [.weakMemory, .objectPointerPersonality], valueOptions: .strongMemory)
  static func normalized(_ source: NSImage, pixels: Int = 64) -> NSImage {
    if let saved = cache.object(forKey: source), saved.pixels == pixels { return saved.image }

    guard source.size.width > 0, source.size.height > 0,
      let bitmap = NSBitmapImageRep(
        bitmapDataPlanes: nil, pixelsWide: pixels,
        pixelsHigh: pixels, bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true,
        isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: pixels * 4, bitsPerPixel: 32),
      let context = NSGraphicsContext(bitmapImageRep: bitmap)
    else { return source }
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = context
    NSColor.clear.setFill()
    NSRect(x: 0, y: 0, width: pixels, height: pixels).fill(using: .copy)
    let scale = CGFloat(pixels) / max(source.size.width, source.size.height)
    let width = source.size.width * scale
    let height = source.size.height * scale
    source.draw(
      in: NSRect(
        x: (CGFloat(pixels) - width) / 2, y: (CGFloat(pixels) - height) / 2, width: width,
        height: height),
      from: .zero, operation: .sourceOver, fraction: 1)
    context.flushGraphics()
    NSGraphicsContext.restoreGraphicsState()
    guard let data = bitmap.bitmapData else { return source }
    var left = pixels
    var top = pixels
    var right = -1
    var bottom = -1
    for y in 0..<pixels {
      for x in 0..<pixels {
        if data[y * bitmap.bytesPerRow + x * 4 + 3] >= 128 {
          left = min(left, x)
          top = min(top, y)
          right = max(right, x)
          bottom = max(bottom, y)
        }
      }
    }
    guard right >= left, bottom >= top, let cg = bitmap.cgImage,
      let cropped = cg.cropping(
        to: CGRect(x: left, y: top, width: right - left + 1, height: bottom - top + 1))
    else { return source }
    let result = NSImage(
      cgImage: cropped, size: NSSize(width: right - left + 1, height: bottom - top + 1))
    result.isTemplate = source.isTemplate
    if cache.count >= 256 { cache.removeAllObjects() }
    cache.setObject(Cached(pixels: pixels, image: result), forKey: source)
    return result
  }
}
