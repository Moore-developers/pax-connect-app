import AppKit
import PaxCore
import PaxUI

extension InterfaceCopyBindings {
  func usageLabel(
    _ key: String, secondary: Bool = false, metric: Bool = false, numeric: Bool = false
  ) -> NSTextField {
    bind(AIUsageLayout.label("", secondary: secondary, metric: metric, numeric: numeric), key: key)
    {
      $0.stringValue = $1
    }
  }
  func usageHeading(_ key: String) -> NSTextField {
    bind(AIUsageLayout.heading(""), key: key) { $0.stringValue = $1 }
  }
  func usageHeader(
    symbol: String, titleKey: String, sub: NSView? = nil, trailing: [NSView] = []
  ) -> NSStackView {
    AIUsageLayout.cardHeader(
      symbol: symbol, title: "", sub: sub, trailing: trailing, titleField: usageHeading(titleKey))
  }
}
