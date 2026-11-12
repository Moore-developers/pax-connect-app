import AppKit

extension PaxTheme {
  public enum MenuBar {
    public static let latencyTabStop: CGFloat = 310
    public static let maximumNameCharacters = 24
    public static var latencyFont: NSFont {
      .monospacedDigitSystemFont(ofSize: NSFont.menuFont(ofSize: 0).pointSize, weight: .regular)
    }
  }
}
