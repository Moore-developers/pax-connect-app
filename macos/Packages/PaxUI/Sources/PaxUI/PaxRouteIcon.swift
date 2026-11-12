import AppKit

/// 与已确认原稿同源的 Lucide 1.17 矢量图标。NSImage 原生解码 SVG，无 WebView。
@MainActor
public enum PaxRouteIcon {
  private static let images = NSCache<NSString, NSImage>()

  private static let names: [String: String] = [
    "point.3.connected.trianglepath.dotted": "route", "square.grid.2x2": "layout-dashboard",
    "cloud": "cloud", "network": "network", "server.rack": "server", "desktopcomputer": "monitor",
    "globe": "globe", "timer": "timer", "arrow.down": "download",
    "arrow.up.right": "arrow-up-right",
    "arrow.down.left": "arrow-down-left", "pencil": "pencil", "curlybraces": "file-json",
    "slider.horizontal.3": "sliders-horizontal", "xmark": "x", "chevron.right": "chevron-right",
    "chevron.down": "chevron-down", "checkmark": "check", "tag": "tag",
    "arrow.clockwise": "refresh-cw",
    "arrow.left.arrow.right": "arrow-right-left", "info.circle": "info", "trash": "trash-2",
    "chart.xyaxis.line": "chart-no-axes-combined", "sparkles": "sparkles", "plus": "plus",
    "arrow.up.arrow.down": "arrow-up-down", "waveform.path.ecg": "activity",
    "square.and.arrow.down": "download", "magnifyingglass": "search", "list.bullet": "list",
    "arrow.triangle.branch": "git-branch", "gearshape": "settings",
  ]
  public static func image(_ symbol: String, size: CGFloat = PaxTheme.Control.navigationIcon)
    -> NSImage?
  {
    let key = "\(symbol):\(size)" as NSString
    if let cached = images.object(forKey: key) { return cached.copy() as? NSImage }
    guard let name = names[symbol],
      let url = Bundle.module.url(
        forResource: name, withExtension: "svg", subdirectory: "RouteIcons"),
      let image = NSImage(contentsOf: url)
    else { return PaxIcon.image(symbol, size: size) }
    image.size = NSSize(width: size, height: size)
    image.isTemplate = true
    images.setObject(image, forKey: key)
    // NSImage is mutable: consumers receive their own size/template state.
    return image.copy() as? NSImage
  }
  public static func view(_ symbol: String, size: CGFloat = PaxTheme.Control.navigationIcon)
    -> NSImageView
  {
    let view = NSImageView()
    view.image = image(symbol, size: size)
    view.contentTintColor = PaxTheme.Routes.muted
    return view
  }
}
