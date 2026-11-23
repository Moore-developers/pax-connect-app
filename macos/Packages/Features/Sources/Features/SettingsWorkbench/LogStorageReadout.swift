import AppKit
import PaxCore
import PaxUI

@MainActor enum LogStorageReadout {
  private struct Section {
    let title: String
    let color: NSColor
    let rows: [(title: String, bytes: UInt64)]

    var bytes: UInt64 { rows.reduce(0) { $0 + $1.bytes } }
  }

  static func view(_ usage: SQLiteStorageUsage, context: PresentationContext) -> NSView {
    let sections = sections(usage, context: context)
    let title = heading(context.text("settings.logs.storage_title"))
    let total = PaxText.label(
      TrafficFormatting.bytes(usage.totalBytes),
      font: PaxTheme.Typography.metric(size: PaxTheme.Chart.proportionLabel, weight: .light))
    let share = PaxShareBar(
      segments: sections.map { .init($0.title, value: Double($0.bytes)) },
      colors: sections.map(\.color))
    share.heightAnchor.constraint(equalToConstant: PaxTheme.Chart.trafficShareBar).isActive = true
    let body = column(
      [
        title, total, share, separator(), sectionView(sections[0]), separator(),
        sectionView(sections[1]),
      ], spacing: PaxTheme.Spacing.medium)
    body.widthAnchor.constraint(equalToConstant: PaxTheme.Settings.storageReadoutWidth)
      .isActive = true
    return body
  }

  static func accessibilityText(_ usage: SQLiteStorageUsage, context: PresentationContext) -> String
  {
    sections(usage, context: context).map { section in
      ([section.title] + section.rows.map { $0.title + " " + TrafficFormatting.bytes($0.bytes) })
        .joined(separator: "\n")
    }.joined(separator: "\n\n")
  }

  private static func sections(_ usage: SQLiteStorageUsage, context: PresentationContext)
    -> [Section]
  {
    [
      Section(
        title: context.text("settings.logs.storage_network"), color: PaxTheme.Color.accent,
        rows: [
          (context.text("settings.logs.storage_logs"), usage.logsBytes),
          (context.text("settings.logs.storage_traffic"), usage.trafficBytes),
          (context.text("settings.logs.storage_journal"), usage.journalBytes),
        ]),
      Section(
        title: context.text("settings.logs.storage_ai"), color: PaxTheme.DashboardColor.green,
        rows: [
          (context.text("settings.logs.storage_ai_usage"), usage.aiUsageBytes),
          (context.text("settings.logs.storage_profile"), usage.profileEventsBytes),
        ]),
    ]
  }

  private static func sectionView(_ section: Section) -> NSView {
    let label = row(
      [PaxLegendDot(color: section.color), heading(section.title), NSView()],
      spacing: PaxTheme.Spacing.tight)
    let rows = section.rows.map { entry in
      let name = PaxText.label(entry.title, font: PaxTheme.Typography.caption(), secondary: true)
      name.lineBreakMode = .byTruncatingTail
      let bytes = PaxText.label(
        TrafficFormatting.bytes(entry.bytes), font: PaxTheme.Typography.time)
      bytes.setContentCompressionResistancePriority(.required, for: .horizontal)
      return row([name, NSView(), bytes], spacing: PaxTheme.Spacing.tight)
    }
    return column([label] + rows, spacing: PaxTheme.Spacing.tight)
  }

  private static func heading(_ text: String) -> NSTextField {
    PaxText.label(
      text, font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyTitle, weight: .medium))
  }

  private static func separator() -> NSBox {
    let line = NSBox()
    line.boxType = .separator
    return line
  }

  private static func column(_ views: [NSView], spacing: CGFloat) -> NSStackView {
    let stack = NSStackView(views: views)
    stack.orientation = .vertical
    stack.alignment = .leading
    stack.spacing = spacing
    for view in views { view.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true }
    return stack
  }

  private static func row(_ views: [NSView], spacing: CGFloat) -> NSStackView {
    let stack = NSStackView(views: views)
    stack.alignment = .centerY
    stack.spacing = spacing
    return stack
  }
}
