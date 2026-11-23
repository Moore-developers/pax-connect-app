import AppKit
import PaxUI

final class FlippedDocumentView: NSView {
  override var isFlipped: Bool { true }
}

final class ConnectionReadoutFillView: NSView {
  private let fill: NSColor
  private let radius: CGFloat
  init(fill: NSColor, radius: CGFloat) {
    self.fill = fill
    self.radius = radius
    super.init(frame: .zero)
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }
  override func draw(_ dirtyRect: NSRect) {
    fill.setFill()
    NSBezierPath(roundedRect: bounds, xRadius: radius, yRadius: radius).fill()
  }
}

final class ConnectionReadoutHairlineView: NSView {
  init(vertical: Bool) {
    super.init(frame: .zero)
    if vertical {
      widthAnchor.constraint(equalToConstant: PaxTheme.Layout.cardBorder).isActive = true
    } else {
      heightAnchor.constraint(equalToConstant: PaxTheme.Layout.cardBorder).isActive = true
    }
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }
  override func draw(_ dirtyRect: NSRect) {
    PaxTheme.TrafficDetail.hoverDivider.setFill()
    bounds.fill()
  }
}

final class ConnectionReadoutDotView: NSView {
  private let color: NSColor
  init(color: NSColor, size: CGFloat) {
    self.color = color
    super.init(frame: .zero)
    NSLayoutConstraint.activate([
      widthAnchor.constraint(equalToConstant: size),
      heightAnchor.constraint(equalToConstant: size),
    ])
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }
  override func draw(_ dirtyRect: NSRect) {
    color.setFill()
    NSBezierPath(ovalIn: bounds).fill()
  }
}

final class ConnectionExitShareRowView: NSView {
  private let fraction: CGFloat
  private let tone: WorkbenchConnectionReadoutLayout.Tone

  init(
    name: String, count: String, share: String, fraction: CGFloat,
    tone: WorkbenchConnectionReadoutLayout.Tone
  ) {
    self.fraction = fraction
    self.tone = tone
    super.init(frame: .zero)
    let theme = PaxTheme.TrafficDetail.self
    heightAnchor.constraint(equalToConstant: theme.hoverExitRowHeight).isActive = true

    let dot = ConnectionReadoutDotView(color: tone.dot, size: theme.hoverStatusDotSize)
    let nameLabel = PaxText.label(
      name, font: PaxTheme.Typography.label(size: theme.hoverBodyFont, weight: .medium))
    nameLabel.lineBreakMode = .byTruncatingTail
    nameLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)

    let countLabel = PaxText.label(
      count, font: PaxTheme.Typography.metric(size: theme.hoverBodyFont, weight: .medium))
    countLabel.setContentHuggingPriority(.required, for: .horizontal)
    countLabel.setContentCompressionResistancePriority(.required, for: .horizontal)

    let shareLabel = PaxText.label(
      "(" + share + ")", font: PaxTheme.Typography.metric(size: theme.hoverMetaFont),
      secondary: true)
    shareLabel.setContentHuggingPriority(.required, for: .horizontal)
    shareLabel.setContentCompressionResistancePriority(.required, for: .horizontal)

    for child in [dot, nameLabel, countLabel, shareLabel] {
      child.translatesAutoresizingMaskIntoConstraints = false
      addSubview(child)
    }
    NSLayoutConstraint.activate([
      dot.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 10),
      dot.centerYAnchor.constraint(equalTo: centerYAnchor),
      nameLabel.leadingAnchor.constraint(equalTo: dot.trailingAnchor, constant: 8),
      nameLabel.centerYAnchor.constraint(equalTo: centerYAnchor),
      nameLabel.trailingAnchor.constraint(
        lessThanOrEqualTo: countLabel.leadingAnchor, constant: -8),
      countLabel.centerYAnchor.constraint(equalTo: centerYAnchor),
      shareLabel.leadingAnchor.constraint(equalTo: countLabel.trailingAnchor, constant: 4),
      shareLabel.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -10),
      shareLabel.firstBaselineAnchor.constraint(equalTo: countLabel.firstBaselineAnchor),
    ])
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }

  override func draw(_ dirtyRect: NSRect) {
    let radius = PaxTheme.TrafficDetail.hoverBlockRadius
    let track = NSBezierPath(roundedRect: bounds, xRadius: radius, yRadius: radius)
    PaxTheme.TrafficDetail.hoverSubtleFill.setFill()
    track.fill()
    guard fraction > 0 else { return }
    NSGraphicsContext.saveGraphicsState()
    track.addClip()
    tone.fill.setFill()
    NSRect(x: 0, y: 0, width: bounds.width * fraction, height: bounds.height).fill()
    NSGraphicsContext.restoreGraphicsState()
  }
}

final class ConnectionChipFlowView: NSView {
  private let chips: [NSView]
  private let availableWidth: CGFloat
  private let alignTrailing: Bool
  private let gapX: CGFloat = 6
  private let gapY: CGFloat = 6
  private var measuredSize = NSSize.zero

  init(chips: [NSView], availableWidth: CGFloat, alignTrailing: Bool) {
    self.chips = chips
    self.availableWidth = max(1, availableWidth)
    self.alignTrailing = alignTrailing
    super.init(frame: .zero)
    for chip in chips {
      chip.translatesAutoresizingMaskIntoConstraints = true
      addSubview(chip)
    }
    measuredSize = placeChips(applyFrames: false)
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }

  override var isFlipped: Bool { true }
  override var intrinsicContentSize: NSSize { measuredSize }

  override func layout() {
    super.layout()
    _ = placeChips(applyFrames: true)
  }

  private func placeChips(applyFrames: Bool) -> NSSize {
    let width = bounds.width > 0 ? bounds.width : availableWidth
    var rows: [[(view: NSView, size: NSSize)]] = [[]]
    var rowWidth: CGFloat = 0
    for chip in chips {
      let size = chip.fittingSize
      let next = rowWidth == 0 ? size.width : rowWidth + gapX + size.width
      if rowWidth > 0 && next > width {
        rows.append([(chip, size)])
        rowWidth = size.width
      } else {
        rows[rows.count - 1].append((chip, size))
        rowWidth = next
      }
    }
    var y: CGFloat = 0
    var maxUsedWidth: CGFloat = 0
    for row in rows where !row.isEmpty {
      let total =
        row.reduce(CGFloat(0)) { $0 + $1.size.width } + CGFloat(max(0, row.count - 1)) * gapX
      let height = row.map(\.size.height).max() ?? 0
      var x = alignTrailing ? max(0, width - total) : 0
      for item in row {
        if applyFrames {
          item.view.frame = NSRect(
            x: x, y: y + (height - item.size.height) / 2,
            width: item.size.width, height: item.size.height)
        }
        x += item.size.width + gapX
      }
      maxUsedWidth = max(maxUsedWidth, total)
      y += height + gapY
    }
    let totalHeight = max(0, y - gapY)
    return NSSize(width: min(width, maxUsedWidth), height: totalHeight)
  }
}
