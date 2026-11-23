import AppKit
import PaxCore
import PaxUI

/// Local layout helpers for the interactive connection-target inspector.
@MainActor enum WorkbenchConnectionReadoutLayout {
  enum Tone {
    case green, amber, red, neutral

    var dot: NSColor {
      switch self {
      case .green: PaxTheme.DashboardColor.green
      case .amber: PaxTheme.DashboardColor.amber
      case .red: PaxTheme.DashboardColor.red
      case .neutral: PaxTheme.Color.textSecondary
      }
    }
    var fill: NSColor {
      switch self {
      case .green: PaxTheme.TrafficDetail.hoverGreenFill
      case .amber: PaxTheme.TrafficDetail.hoverAmberFill
      case .red: PaxTheme.TrafficDetail.hoverRedFill
      case .neutral: PaxTheme.TrafficDetail.hoverChipNeutralFill
      }
    }
    var chipText: NSColor {
      switch self {
      case .green: PaxTheme.TrafficDetail.hoverGreenText
      case .amber: PaxTheme.TrafficDetail.hoverAmberText
      case .red: PaxTheme.TrafficDetail.hoverRedText
      case .neutral: PaxTheme.Color.textSecondary
      }
    }
  }

  static func stack(_ views: [NSView] = [], gap: CGFloat = PaxTheme.Spacing.tight) -> NSStackView {
    let stack = NSStackView(views: views)
    stack.orientation = .vertical
    stack.alignment = .leading
    stack.spacing = gap
    for view in views { view.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true }
    return stack
  }

  static func hairline() -> NSView { ConnectionReadoutHairlineView(vertical: false) }

  static func header(_ group: TrafficConnectionGroup, context: PresentationContext, action: NSView?)
    -> NSView
  {
    let theme = PaxTheme.TrafficDetail.self
    let name = group.kind == "unknown" ? context.text("connection_groups.unknown") : group.target
    let title = PaxText.label(
      name, font: PaxTheme.Typography.label(size: theme.hoverTitleFont, weight: .semibold),
      wrapping: true)
    title.lineBreakMode = .byCharWrapping
    title.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)

    let time = TrafficFormatting.dateAndClock(group.lastAt)
    let last = PaxText.label(
      context.text("connection_groups.last") + "  " + time.date + " " + time.clock,
      font: PaxTheme.Typography.time, secondary: true)
    last.lineBreakMode = .byTruncatingTail

    let identity = stack([title, last], gap: PaxTheme.Spacing.xs)
    let row = NSView()
    identity.translatesAutoresizingMaskIntoConstraints = false
    row.addSubview(identity)
    NSLayoutConstraint.activate([
      identity.leadingAnchor.constraint(equalTo: row.leadingAnchor),
      identity.topAnchor.constraint(equalTo: row.topAnchor),
      identity.bottomAnchor.constraint(equalTo: row.bottomAnchor),
    ])
    if let action {
      action.translatesAutoresizingMaskIntoConstraints = false
      action.setContentHuggingPriority(.required, for: .horizontal)
      action.setContentCompressionResistancePriority(.required, for: .horizontal)
      row.addSubview(action)
      NSLayoutConstraint.activate([
        identity.trailingAnchor.constraint(
          equalTo: action.leadingAnchor, constant: -PaxTheme.Spacing.medium),
        action.trailingAnchor.constraint(equalTo: row.trailingAnchor),
        action.topAnchor.constraint(equalTo: row.topAnchor),
      ])
    } else {
      identity.trailingAnchor.constraint(equalTo: row.trailingAnchor).isActive = true
    }
    return row
  }

  /// Four-column unified KPI strip; returns the latency reading so chart hover mirrors it live.
  static func metrics(
    _ group: TrafficConnectionGroup, latestLatency: String, context: PresentationContext
  ) -> (view: NSView, latencyValue: NSTextField) {
    let theme = PaxTheme.TrafficDetail.self
    func cell(_ key: String, value: String, valueColor: NSColor) -> (NSView, NSTextField) {
      let label = PaxText.label(
        context.text(key), font: PaxTheme.Typography.label(size: theme.hoverMetaFont),
        secondary: true)
      label.lineBreakMode = .byTruncatingTail
      let reading = PaxText.label(
        value, font: PaxTheme.Typography.metric(size: theme.hoverMetricFont, weight: .semibold))
      reading.textColor = valueColor
      reading.lineBreakMode = .byTruncatingTail
      let box = NSView()
      for child in [label, reading] {
        child.translatesAutoresizingMaskIntoConstraints = false
        box.addSubview(child)
      }
      NSLayoutConstraint.activate([
        label.leadingAnchor.constraint(equalTo: box.leadingAnchor, constant: 10),
        label.trailingAnchor.constraint(equalTo: box.trailingAnchor, constant: -8),
        label.topAnchor.constraint(equalTo: box.topAnchor, constant: 8),
        reading.leadingAnchor.constraint(equalTo: label.leadingAnchor),
        reading.trailingAnchor.constraint(equalTo: label.trailingAnchor),
        reading.topAnchor.constraint(equalTo: label.bottomAnchor, constant: 3),
        reading.bottomAnchor.constraint(equalTo: box.bottomAnchor, constant: -8),
      ])
      return (box, reading)
    }

    let known = group.bytesState != "unknown"
    let c0 = cell(
      "connection_groups.connections", value: String(group.connections),
      valueColor: PaxTheme.Color.text)
    let c1 = cell(
      "dashboard.upload", value: known ? TrafficFormatting.bytes(group.upload) : "—",
      valueColor: theme.hoverAmberText)
    let c2 = cell(
      "dashboard.download", value: known ? TrafficFormatting.bytes(group.download) : "—",
      valueColor: theme.hoverGreenText)
    let c3 = cell(
      "connection_groups.latest_latency", value: latestLatency, valueColor: PaxTheme.Color.text)

    let strip = ConnectionReadoutFillView(
      fill: theme.hoverSubtleFill, radius: theme.hoverStripRadius)
    let cells = [c0.0, c1.0, c2.0, c3.0]
    var previousAnchor = strip.leadingAnchor
    for (index, cellView) in cells.enumerated() {
      cellView.translatesAutoresizingMaskIntoConstraints = false
      strip.addSubview(cellView)
      NSLayoutConstraint.activate([
        cellView.leadingAnchor.constraint(equalTo: previousAnchor),
        cellView.topAnchor.constraint(equalTo: strip.topAnchor),
        cellView.bottomAnchor.constraint(equalTo: strip.bottomAnchor),
      ])
      if index > 0 {
        cellView.widthAnchor.constraint(equalTo: cells[0].widthAnchor).isActive = true
      }
      if index < cells.count - 1 {
        let divider = ConnectionReadoutHairlineView(vertical: true)
        divider.translatesAutoresizingMaskIntoConstraints = false
        strip.addSubview(divider)
        NSLayoutConstraint.activate([
          divider.leadingAnchor.constraint(equalTo: cellView.trailingAnchor),
          divider.topAnchor.constraint(equalTo: strip.topAnchor, constant: 8),
          divider.bottomAnchor.constraint(equalTo: strip.bottomAnchor, constant: -8),
        ])
        previousAnchor = divider.trailingAnchor
      } else {
        cellView.trailingAnchor.constraint(equalTo: strip.trailingAnchor).isActive = true
      }
    }

    if group.bytesState == "partial" || !known {
      let key = known ? "connection_groups.partial_bytes" : "connection_groups.unknown_bytes"
      let note = PaxText.label(
        context.text(key), font: PaxTheme.Typography.label(size: theme.hoverMetaFont),
        secondary: true, wrapping: true)
      return (stack([strip, note], gap: PaxTheme.Spacing.xs), c3.1)
    }
    return (strip, c3.1)
  }

  static func sectionHeader(_ title: String, trailing: String? = nil)
    -> (view: NSView, trailingLabel: NSTextField?)
  {
    let theme = PaxTheme.TrafficDetail.self
    let left = PaxText.label(
      title, font: PaxTheme.Typography.label(size: theme.hoverMetaFont, weight: .medium),
      secondary: true)
    let row = NSView()
    left.translatesAutoresizingMaskIntoConstraints = false
    row.addSubview(left)
    NSLayoutConstraint.activate([
      left.leadingAnchor.constraint(equalTo: row.leadingAnchor),
      left.topAnchor.constraint(equalTo: row.topAnchor),
      left.bottomAnchor.constraint(equalTo: row.bottomAnchor),
    ])
    guard let trailing else {
      left.trailingAnchor.constraint(lessThanOrEqualTo: row.trailingAnchor).isActive = true
      return (row, nil)
    }
    let right = PaxText.label(
      trailing, font: PaxTheme.Typography.metric(size: theme.hoverMetaFont), secondary: true)
    right.alignment = .right
    right.setContentHuggingPriority(.required, for: .horizontal)
    right.setContentCompressionResistancePriority(.required, for: .horizontal)
    right.translatesAutoresizingMaskIntoConstraints = false
    row.addSubview(right)
    NSLayoutConstraint.activate([
      left.trailingAnchor.constraint(lessThanOrEqualTo: right.leadingAnchor, constant: -8),
      right.trailingAnchor.constraint(equalTo: row.trailingAnchor),
      right.centerYAnchor.constraint(equalTo: left.centerYAnchor),
    ])
    return (row, right)
  }

  static func exitShareRow(name: String, count: UInt64, total: UInt64, tone: Tone) -> NSView {
    let fraction = total > 0 ? min(1, max(0, CGFloat(count) / CGFloat(total))) : 0
    let pctText: String = {
      guard total > 0 else { return "0%" }
      let pct = Int((Double(count) / Double(total) * 100).rounded())
      return pct == 0 && count > 0 ? "<1%" : "\(pct)%"
    }()
    return ConnectionExitShareRowView(
      name: name, count: String(count), share: pctText, fraction: fraction, tone: tone)
  }

  static func codeBadge(_ text: String, maxWidth: CGFloat) -> NSView {
    let theme = PaxTheme.TrafficDetail.self
    let font = NSFont.monospacedSystemFont(ofSize: theme.hoverCodeFont, weight: .regular)
    let label = PaxText.label(text, font: font, secondary: true, wrapping: true)
    label.lineBreakMode = .byCharWrapping
    label.preferredMaxLayoutWidth = max(40, maxWidth - 12)
    let box = ConnectionReadoutFillView(fill: theme.hoverSubtleFill, radius: theme.hoverPillRadius)
    label.translatesAutoresizingMaskIntoConstraints = false
    box.addSubview(label)
    NSLayoutConstraint.activate([
      label.leadingAnchor.constraint(equalTo: box.leadingAnchor, constant: 6),
      label.trailingAnchor.constraint(equalTo: box.trailingAnchor, constant: -6),
      label.topAnchor.constraint(equalTo: box.topAnchor, constant: 2),
      label.bottomAnchor.constraint(equalTo: box.bottomAnchor, constant: -2),
    ])
    return box
  }

  static func statusChip(label: String, count: UInt64, tone: Tone) -> NSView {
    let theme = PaxTheme.TrafficDetail.self
    let dot = ConnectionReadoutDotView(color: tone.dot, size: theme.hoverStatusDotSize)
    let text = PaxText.label(
      label, font: PaxTheme.Typography.label(size: theme.hoverMetaFont, weight: .medium))
    text.textColor = tone.chipText
    let num = PaxText.label(
      String(count),
      font: PaxTheme.Typography.metric(size: theme.hoverMetaFont, weight: .semibold))
    num.textColor = tone.chipText

    let chip = ConnectionReadoutFillView(fill: tone.fill, radius: theme.hoverPillRadius)
    for child in [dot, text, num] {
      child.translatesAutoresizingMaskIntoConstraints = false
      chip.addSubview(child)
    }
    NSLayoutConstraint.activate([
      dot.leadingAnchor.constraint(equalTo: chip.leadingAnchor, constant: 7),
      dot.centerYAnchor.constraint(equalTo: chip.centerYAnchor),
      text.leadingAnchor.constraint(equalTo: dot.trailingAnchor, constant: 5),
      text.topAnchor.constraint(equalTo: chip.topAnchor, constant: 3),
      text.bottomAnchor.constraint(equalTo: chip.bottomAnchor, constant: -3),
      num.leadingAnchor.constraint(equalTo: text.trailingAnchor, constant: 4),
      num.trailingAnchor.constraint(equalTo: chip.trailingAnchor, constant: -7),
      num.firstBaselineAnchor.constraint(equalTo: text.firstBaselineAnchor),
    ])
    return chip
  }

  static func chipFlow(_ chips: [NSView], width: CGFloat, alignTrailing: Bool) -> NSView {
    ConnectionChipFlowView(chips: chips, availableWidth: width, alignTrailing: alignTrailing)
  }

  static func failureBanner(_ message: String) -> NSView {
    let theme = PaxTheme.TrafficDetail.self
    let label = PaxText.label(
      message, font: PaxTheme.Typography.label(size: theme.hoverMetaFont), wrapping: true)
    label.textColor = theme.hoverRedText
    label.lineBreakMode = .byWordWrapping
    let box = ConnectionReadoutFillView(fill: theme.hoverRedFill, radius: theme.hoverBlockRadius)
    label.translatesAutoresizingMaskIntoConstraints = false
    box.addSubview(label)
    NSLayoutConstraint.activate([
      label.leadingAnchor.constraint(equalTo: box.leadingAnchor, constant: 10),
      label.trailingAnchor.constraint(equalTo: box.trailingAnchor, constant: -10),
      label.topAnchor.constraint(equalTo: box.topAnchor, constant: 7),
      label.bottomAnchor.constraint(equalTo: box.bottomAnchor, constant: -7),
    ])
    return box
  }

  static func insetBlock(title: String, rows: [NSView], gap: CGFloat = 5) -> NSView {
    let theme = PaxTheme.TrafficDetail.self
    let heading = PaxText.label(
      title, font: PaxTheme.Typography.label(size: theme.hoverMetaFont, weight: .medium),
      secondary: true)
    let body = stack([heading] + rows, gap: gap)
    let box = ConnectionReadoutFillView(fill: theme.hoverSubtleFill, radius: theme.hoverBlockRadius)
    body.translatesAutoresizingMaskIntoConstraints = false
    box.addSubview(body)
    NSLayoutConstraint.activate([
      body.leadingAnchor.constraint(equalTo: box.leadingAnchor, constant: 10),
      body.trailingAnchor.constraint(equalTo: box.trailingAnchor, constant: -10),
      body.topAnchor.constraint(equalTo: box.topAnchor, constant: 8),
      body.bottomAnchor.constraint(equalTo: box.bottomAnchor, constant: -8),
    ])
    return box
  }
}
