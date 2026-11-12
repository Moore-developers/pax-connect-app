import AppKit
import PaxCore
import PaxUI

@MainActor
enum AIClientMark {
  static func shortName(_ code: String) -> String {
    switch code {
    case "claude": "Claude"
    case "codex": "Codex"
    case "dsh": "DeepSeek"
    default: code
    }
  }
  private static let brandIcon: CGFloat = 24
  static func image(_ client: AIClient) -> NSImage? {
    let name: String
    switch client {
    case .claude: name = "claude"
    case .codex: name = "codex"
    case .harness: name = "harness"
    }
    guard
      let url = Bundle.module.url(
        forResource: name, withExtension: "png", subdirectory: "AIClients")
    else { return nil }
    return NSImage(contentsOf: url)
      ?? NSImage(systemSymbolName: client.symbol, accessibilityDescription: client.rawValue)
  }
  static func view(_ client: AIClient) -> NSView {
    let image = NSImageView()
    image.image = self.image(client)
    image.imageScaling = .scaleProportionallyUpOrDown
    let side = brandIcon
    image.widthAnchor.constraint(equalToConstant: side).isActive = true
    image.heightAnchor.constraint(equalToConstant: side).isActive = true
    image.setAccessibilityElement(false)
    return image
  }
}
