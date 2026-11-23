import AppKit
import PaxCore
import PaxUI

/// Right-aligned two-line upload and download values for each connection target row.
@MainActor final class WorkbenchConnectionTransferText: NSStackView {
  init(upload: UInt64, download: UInt64, bytesState: String = "complete") {
    let theme = PaxTheme.TrafficDetail.self
    let known = bytesState != "unknown"
    let up = PaxText.label(
      "↑ " + (known ? TrafficFormatting.bytes(upload) : "—"),
      font: PaxTheme.Typography.metric(size: 12, weight: .regular))
    let down = PaxText.label(
      "↓ " + (known ? TrafficFormatting.bytes(download) : "—"),
      font: PaxTheme.Typography.metric(size: 12, weight: .medium))
    up.textColor = theme.hoverAmberText
    down.textColor = theme.hoverGreenText
    up.alignment = .right
    down.alignment = .right
    super.init(frame: .zero)
    orientation = .vertical
    alignment = .trailing
    spacing = 2
    for label in [up, down] {
      label.lineBreakMode = .byTruncatingTail
      label.setContentCompressionResistancePriority(.required, for: .horizontal)
      addArrangedSubview(label)
      label.widthAnchor.constraint(equalTo: widthAnchor).isActive = true
    }
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }
}
