import AppKit

/// SF Symbol 统一入口：字号阶驱动点阵配置，粗细按用途。
/// 业务代码不直接拼 SymbolConfiguration。
@MainActor
public enum PaxIcon {
  public static func image(_ symbol: String, size: CGFloat = 14, weight: NSFont.Weight = .regular)
    -> NSImage?
  {
    guard let base = NSImage(systemSymbolName: symbol, accessibilityDescription: nil) else {
      return nil
    }
    return base.withSymbolConfiguration(PaxTheme.Symbol.config(size: size, weight: weight))
  }

  public static func view(_ symbol: String, size: CGFloat = 14, weight: NSFont.Weight = .regular)
    -> NSImageView
  {
    let view = NSImageView()
    view.image = image(symbol, size: size, weight: weight)
    view.contentTintColor = PaxTheme.Color.textSecondary
    return view
  }
}
