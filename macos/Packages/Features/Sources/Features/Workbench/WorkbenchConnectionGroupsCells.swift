import AppKit
import PaxCore
import PaxUI

/// Four-column rounded KPI summary strip shared in visual DNA with the target hover readout.
@MainActor final class WorkbenchConnectionSummaryStrip: NSView {
  private let targetsValue = WorkbenchConnectionSummaryStrip.valueLabel()
  private let connectionsValue = WorkbenchConnectionSummaryStrip.valueLabel()
  private let loadedValue = WorkbenchConnectionSummaryStrip.valueLabel()
  private let shownValue = WorkbenchConnectionSummaryStrip.valueLabel()
  private let helpReadout = PaxHoverReadout()
  private let context: PresentationContext

  init(context: PresentationContext) {
    self.context = context
    super.init(frame: .zero)
    let theme = PaxTheme.TrafficDetail.self

    let helpBadge = HelpCircleBadge()
    let helpHost = PaxHoverReadoutHost(helpBadge, readout: helpReadout) { [context] in
      Self.hintCard(context: context)
    }
    helpHost.setAccessibilityElement(true)
    helpHost.setAccessibilityRole(.button)
    helpHost.setAccessibilityLabel(context.text("connection_groups.filter_hint_title"))
    helpHost.setAccessibilityHelp(context.text("connection_groups.local_filter_note"))

    let cells: [NSView] = [
      Self.metricCell(context.text("connection_groups.total_targets"), value: targetsValue),
      Self.metricCell(context.text("connection_groups.total_connections"), value: connectionsValue),
      Self.metricCell(context.text("connection_groups.loaded_targets"), value: loadedValue),
      Self.metricCell(
        context.text("connection_groups.shown_targets"), value: shownValue, accessory: helpHost),
    ]

    var previous = leadingAnchor
    for (index, cell) in cells.enumerated() {
      cell.translatesAutoresizingMaskIntoConstraints = false
      addSubview(cell)
      NSLayoutConstraint.activate([
        cell.leadingAnchor.constraint(equalTo: previous),
        cell.topAnchor.constraint(equalTo: topAnchor),
        cell.bottomAnchor.constraint(equalTo: bottomAnchor),
      ])
      if index > 0 {
        cell.widthAnchor.constraint(equalTo: cells[0].widthAnchor).isActive = true
      }
      if index < cells.count - 1 {
        let divider = ConnectionReadoutHairlineView(vertical: true)
        divider.translatesAutoresizingMaskIntoConstraints = false
        addSubview(divider)
        NSLayoutConstraint.activate([
          divider.leadingAnchor.constraint(equalTo: cell.trailingAnchor),
          divider.topAnchor.constraint(equalTo: topAnchor, constant: 9),
          divider.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -9),
        ])
        previous = divider.trailingAnchor
      } else {
        cell.trailingAnchor.constraint(equalTo: trailingAnchor).isActive = true
      }
    }
    setAccessibilityElement(false)
    _ = theme
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }

  func stop() { helpReadout.close() }

  func update(targets: UInt64, connections: UInt64, loaded: Int, shown: Int) {
    let theme = PaxTheme.TrafficDetail.self
    targetsValue.stringValue = String(targets)
    connectionsValue.stringValue = String(connections)
    let loadedAttr = NSMutableAttributedString(
      string: String(loaded),
      attributes: [
        .font: PaxTheme.Typography.metric(size: theme.hoverTitleFont, weight: .semibold),
        .foregroundColor: PaxTheme.Color.text,
      ])
    loadedAttr.append(
      NSAttributedString(
        string: " / " + String(targets),
        attributes: [
          .font: PaxTheme.Typography.metric(size: theme.hoverMetaFont),
          .foregroundColor: PaxTheme.Color.textSecondary,
        ]))
    loadedValue.attributedStringValue = loadedAttr
    shownValue.stringValue = String(shown)
  }

  override func draw(_ dirtyRect: NSRect) {
    let theme = PaxTheme.TrafficDetail.self
    let rect = bounds.insetBy(dx: 0.5, dy: 0.5)
    let path = NSBezierPath(
      roundedRect: rect, xRadius: theme.hoverStripRadius, yRadius: theme.hoverStripRadius)
    theme.hoverSubtleFill.setFill()
    path.fill()
    theme.hoverDivider.setStroke()
    path.lineWidth = PaxTheme.Layout.cardBorder
    path.stroke()
  }

  private static func valueLabel() -> NSTextField {
    let label = PaxText.label(
      "0",
      font: PaxTheme.Typography.metric(
        size: PaxTheme.TrafficDetail.hoverTitleFont, weight: .semibold))
    label.lineBreakMode = .byTruncatingTail
    return label
  }

  private static func metricCell(
    _ title: String, value: NSTextField, accessory: NSView? = nil
  ) -> NSView {
    let label = PaxText.label(
      title, font: PaxTheme.Typography.label(size: PaxTheme.TrafficDetail.hoverMetaFont),
      secondary: true)
    label.lineBreakMode = .byTruncatingTail
    let box = NSView()
    for child in [label, value] {
      child.translatesAutoresizingMaskIntoConstraints = false
      box.addSubview(child)
    }
    NSLayoutConstraint.activate([
      label.leadingAnchor.constraint(equalTo: box.leadingAnchor, constant: 12),
      label.topAnchor.constraint(equalTo: box.topAnchor, constant: 8),
      value.leadingAnchor.constraint(equalTo: label.leadingAnchor),
      value.trailingAnchor.constraint(equalTo: box.trailingAnchor, constant: -10),
      value.topAnchor.constraint(equalTo: label.bottomAnchor, constant: 3),
      value.bottomAnchor.constraint(equalTo: box.bottomAnchor, constant: -8),
    ])
    if let accessory {
      accessory.translatesAutoresizingMaskIntoConstraints = false
      box.addSubview(accessory)
      NSLayoutConstraint.activate([
        label.trailingAnchor.constraint(lessThanOrEqualTo: accessory.leadingAnchor, constant: -6),
        accessory.trailingAnchor.constraint(equalTo: box.trailingAnchor, constant: -10),
        accessory.centerYAnchor.constraint(equalTo: label.centerYAnchor),
      ])
    } else {
      label.trailingAnchor.constraint(equalTo: box.trailingAnchor, constant: -10).isActive = true
    }
    return box
  }

  private static func hintCard(context: PresentationContext) -> NSView {
    let title = PaxText.label(
      context.text("connection_groups.filter_hint_title"),
      font: PaxTheme.Typography.label(size: 12.5, weight: .semibold))
    let note = PaxText.label(
      context.text("connection_groups.local_filter_note"),
      font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
    let stack = WorkbenchConnectionReadoutLayout.stack([title, note], gap: 4)
    stack.widthAnchor.constraint(equalToConstant: 260).isActive = true
    return stack
  }
}

private final class HelpCircleBadge: NSView {
  init() {
    super.init(frame: .zero)
    NSLayoutConstraint.activate([
      widthAnchor.constraint(equalToConstant: 16),
      heightAnchor.constraint(equalToConstant: 16),
    ])
    let mark = PaxText.label("?", font: PaxTheme.Typography.label(size: 10, weight: .semibold))
    mark.textColor = PaxTheme.Color.textSecondary
    mark.alignment = .center
    mark.translatesAutoresizingMaskIntoConstraints = false
    addSubview(mark)
    NSLayoutConstraint.activate([
      mark.centerXAnchor.constraint(equalTo: centerXAnchor),
      mark.centerYAnchor.constraint(equalTo: centerYAnchor),
    ])
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }
  override func draw(_ dirtyRect: NSRect) {
    PaxTheme.TrafficDetail.hoverChipNeutralFill.setFill()
    NSBezierPath(ovalIn: bounds).fill()
  }
}

/// Primary target cell: domain/IP title above compact time and failure/active indicators.
@MainActor final class WorkbenchConnectionTargetCell: NSStackView {
  init(
    name: String, group: TrafficConnectionGroup, context: PresentationContext
  ) {
    super.init(frame: .zero)
    orientation = .vertical
    alignment = .leading
    spacing = 2

    let title = PaxText.label(
      name, font: PaxTheme.Typography.label(size: 13, weight: .medium))
    title.lineBreakMode = .byTruncatingMiddle
    title.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)

    let failed = group.results.filter { $0.state == "failed" }.reduce(0) { $0 + $1.connections }
    let active = group.results.filter { $0.state == "active" }.reduce(0) { $0 + $1.connections }
    let meta = ConnectionTargetMetadata(
      clock: TrafficFormatting.dateAndClock(group.lastAt).clock, failed: failed, active: active,
      context: context)

    addArrangedSubview(title)
    setVisibilityPriority(.mustHold, for: title)
    addArrangedSubview(meta)
    title.widthAnchor.constraint(equalTo: widthAnchor).isActive = true
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }

}

/// A compact, measured row: extra table width never becomes space inside a status.
@MainActor private final class ConnectionTargetMetadata: NSView {
  private var pieces: [(view: NSView, width: CGFloat, height: CGFloat, gap: CGFloat)] = []
  override var isFlipped: Bool { true }
  init(clock: String, failed: UInt64, active: UInt64, context: PresentationContext) {
    super.init(frame: .zero)
    let time = PaxText.label(
      clock, font: PaxTheme.Typography.metric(size: PaxTheme.TrafficDetail.hoverMetaFont),
      secondary: true)
    append(time, gap: 0)
    if failed > 0 {
      signal(
        context.text("connection_groups.failed_short", ["count": String(failed)]),
        dot: PaxTheme.DashboardColor.red, ink: PaxTheme.TrafficDetail.hoverRedText, gap: 8)
    }
    if active > 0 {
      signal(
        context.text("connection_groups.active_short", ["count": String(active)]),
        dot: PaxTheme.DashboardColor.green, ink: PaxTheme.TrafficDetail.hoverGreenText,
        gap: failed > 0 ? 6 : 8)
    }
    widthAnchor.constraint(equalToConstant: intrinsicContentSize.width).isActive = true
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }
  private func append(_ view: NSView, gap: CGFloat, size: NSSize? = nil) {
    let size = size ?? view.intrinsicContentSize
    pieces.append((view, ceil(size.width), ceil(size.height), gap))
    addSubview(view)
  }
  private func signal(_ text: String, dot: NSColor, ink: NSColor, gap: CGFloat) {
    let size = PaxTheme.TrafficDetail.hoverStatusDotSize
    append(
      ConnectionReadoutDotView(color: dot, size: size), gap: gap,
      size: NSSize(width: size, height: size))
    let label = PaxText.label(
      text, font: PaxTheme.Typography.metric(size: PaxTheme.TrafficDetail.hoverMetaFont))
    label.textColor = ink
    append(label, gap: 4)
  }
  override var intrinsicContentSize: NSSize {
    NSSize(
      width: pieces.reduce(0) { $0 + $1.width + $1.gap }, height: pieces.map(\.height).max() ?? 0)
  }
  override func layout() {
    super.layout()
    var x: CGFloat = 0
    for piece in pieces {
      x += piece.gap
      piece.view.frame = NSRect(
        x: x, y: (bounds.height - piece.height) / 2, width: piece.width, height: piece.height)
      x += piece.width
    }
  }
}

/// Semantic capsule pill for the primary actual exit plus a compact +N overflow badge.
@MainActor final class WorkbenchConnectionExitCell: NSStackView {
  init(
    exits: [TrafficExit], nameForExit: (TrafficExit) -> String, context: PresentationContext
  ) {
    super.init(frame: .zero)
    orientation = .horizontal
    alignment = .centerY
    spacing = 5

    guard let primary = exits.first else {
      let empty = PaxText.label(
        context.text("connection_groups.unrecorded"),
        font: PaxTheme.Typography.label(size: PaxTheme.TrafficDetail.hoverMetaFont),
        secondary: true)
      addArrangedSubview(empty)
      return
    }

    let tone = Self.tone(for: primary.direction)
    let pill = ExitCapsuleView(name: nameForExit(primary), tone: tone)
    pill.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    addArrangedSubview(pill)

    if exits.count > 1 {
      let extra = OverflowCountPill("+" + String(exits.count - 1))
      extra.setContentHuggingPriority(.required, for: .horizontal)
      extra.setContentCompressionResistancePriority(.required, for: .horizontal)
      addArrangedSubview(extra)
      setVisibilityPriority(.mustHold, for: extra)
    }
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }

  private static func tone(for direction: String) -> WorkbenchConnectionReadoutLayout.Tone {
    switch direction {
    case "proxy", "代理": return .green
    case "direct", "直连": return .amber
    case "blocked", "已阻断": return .red
    default: return .neutral
    }
  }
}

private final class ExitCapsuleView: NSView {
  private let tone: WorkbenchConnectionReadoutLayout.Tone
  init(name: String, tone: WorkbenchConnectionReadoutLayout.Tone) {
    self.tone = tone
    super.init(frame: .zero)
    let dot = ConnectionReadoutDotView(
      color: tone.dot, size: PaxTheme.TrafficDetail.hoverStatusDotSize)
    let label = PaxText.label(
      name, font: PaxTheme.Typography.label(size: 11.5, weight: .medium))
    label.textColor = tone.chipText
    label.lineBreakMode = .byTruncatingTail
    label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    for child in [dot, label] {
      child.translatesAutoresizingMaskIntoConstraints = false
      addSubview(child)
    }
    NSLayoutConstraint.activate([
      dot.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 8),
      dot.centerYAnchor.constraint(equalTo: centerYAnchor),
      label.leadingAnchor.constraint(equalTo: dot.trailingAnchor, constant: 5),
      label.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -8),
      label.topAnchor.constraint(equalTo: topAnchor, constant: 3),
      label.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -3),
    ])
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }
  override func draw(_ dirtyRect: NSRect) {
    let radius = bounds.height / 2
    tone.fill.setFill()
    NSBezierPath(roundedRect: bounds, xRadius: radius, yRadius: radius).fill()
  }
}

private final class OverflowCountPill: NSView {
  init(_ text: String) {
    super.init(frame: .zero)
    let label = PaxText.label(
      text, font: PaxTheme.Typography.metric(size: 10.5, weight: .medium), secondary: true)
    label.setContentCompressionResistancePriority(.required, for: .horizontal)
    label.translatesAutoresizingMaskIntoConstraints = false
    addSubview(label)
    NSLayoutConstraint.activate([
      label.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 6),
      label.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -6),
      label.topAnchor.constraint(equalTo: topAnchor, constant: 3),
      label.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -3),
    ])
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }
  override func draw(_ dirtyRect: NSRect) {
    let radius = bounds.height / 2
    PaxTheme.TrafficDetail.hoverChipNeutralFill.setFill()
    NSBezierPath(roundedRect: bounds, xRadius: radius, yRadius: radius).fill()
  }
}
