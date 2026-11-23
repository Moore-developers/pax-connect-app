import AppKit
import PaxCore
import PaxUI

extension WorkbenchConnectionGroupEvidence {
  static func diagnostics(
    _ group: TrafficConnectionGroup, context: PresentationContext, width: CGFloat
  ) -> NSView {
    var sections: [NSView] = [resultsSection(group, context: context, width: width)]
    if !group.addresses.isEmpty {
      sections.append(addressesBlock(group.addresses, context: context))
    }
    if !group.sources.isEmpty {
      sections.append(sourcesBlock(group.sources, context: context))
    }
    return WorkbenchConnectionReadoutLayout.stack(sections, gap: 8)
  }

  private static func resultsSection(
    _ group: TrafficConnectionGroup, context: PresentationContext, width: CGFloat
  ) -> NSView {
    typealias Layout = WorkbenchConnectionReadoutLayout
    let theme = PaxTheme.TrafficDetail.self
    let titleText = context.text("connection_groups.result")
    let titleFont = PaxTheme.Typography.label(size: theme.hoverMetaFont, weight: .medium)
    let titleLabel = PaxText.label(titleText, font: titleFont, secondary: true)
    titleLabel.setContentHuggingPriority(.required, for: .horizontal)
    titleLabel.setContentCompressionResistancePriority(.required, for: .horizontal)

    var order: [String] = []
    var totals: [String: UInt64] = [:]
    var failures: [(message: String, count: UInt64)] = []
    for item in group.results {
      if totals[item.state] == nil { order.append(item.state) }
      totals[item.state, default: 0] += item.connections
      if item.state == "failed" && !item.failure.isEmpty {
        let localized = FlowPresentation.failure(item.failure, context: context)
        let message =
          localized == item.failure ? localized : localized + "（" + item.failure + "）"
        failures.append((message, item.connections))
      }
    }

    let chips: [NSView] = order.map { state in
      let tone: Layout.Tone = state == "active" ? .green : state == "failed" ? .red : .neutral
      let label = context.text("connection_groups.result_" + state)
      return Layout.statusChip(label: label, count: totals[state] ?? 0, tone: tone)
    }

    let headerView: NSView
    if chips.isEmpty {
      let empty = PaxText.label(
        context.text("connection_groups.unrecorded"),
        font: PaxTheme.Typography.label(size: theme.hoverMetaFont), secondary: true)
      headerView = Layout.stack([titleLabel, empty], gap: 4)
    } else {
      let titleWidth = ceil((titleText as NSString).size(withAttributes: [.font: titleFont]).width)
      let chipsWidth =
        chips.reduce(CGFloat(0)) { $0 + $1.fittingSize.width }
        + CGFloat(max(0, chips.count - 1)) * 6
      if titleWidth + 12 + chipsWidth <= width {
        let chipRow = Layout.chipFlow(chips, width: width - titleWidth - 12, alignTrailing: true)
        let row = NSView()
        titleLabel.translatesAutoresizingMaskIntoConstraints = false
        chipRow.translatesAutoresizingMaskIntoConstraints = false
        row.addSubview(titleLabel)
        row.addSubview(chipRow)
        NSLayoutConstraint.activate([
          titleLabel.leadingAnchor.constraint(equalTo: row.leadingAnchor),
          titleLabel.centerYAnchor.constraint(equalTo: row.centerYAnchor),
          chipRow.leadingAnchor.constraint(
            greaterThanOrEqualTo: titleLabel.trailingAnchor, constant: 12),
          chipRow.trailingAnchor.constraint(equalTo: row.trailingAnchor),
          chipRow.topAnchor.constraint(equalTo: row.topAnchor),
          chipRow.bottomAnchor.constraint(equalTo: row.bottomAnchor),
        ])
        headerView = row
      } else {
        let chipFlow = Layout.chipFlow(chips, width: width, alignTrailing: false)
        headerView = Layout.stack([titleLabel, chipFlow], gap: 6)
      }
    }

    guard !failures.isEmpty else { return headerView }
    let banners = failures.map { failure -> NSView in
      let suffix =
        failures.count > 1
        ? " · " + context.text("connection_groups.times", ["count": String(failure.count)]) : ""
      return Layout.failureBanner(failure.message + suffix)
    }
    return Layout.stack([headerView] + banners, gap: 6)
  }

  private static func addressesBlock(
    _ addresses: [TrafficConnectionAddress], context: PresentationContext
  ) -> NSView {
    let theme = PaxTheme.TrafficDetail.self
    let codeFont = NSFont.monospacedSystemFont(ofSize: theme.hoverCodeFont, weight: .regular)
    let countFont = NSFont.monospacedSystemFont(ofSize: theme.hoverCodeFont, weight: .semibold)
    let rows = addresses.map { value -> NSView in
      let ip = value.ip.isEmpty ? context.text("connection_groups.ip_missing") : value.ip
      let ipLabel = PaxText.label(ip, font: codeFont)
      ipLabel.lineBreakMode = .byTruncatingMiddle
      ipLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)

      let protoPort: String = {
        let proto =
          value.transport.isEmpty
          ? context.text("connection_groups.protocol_missing") : value.transport.uppercased()
        if !value.transport.isEmpty && value.port > 0 { return proto + ":" + String(value.port) }
        let port =
          value.port == 0
          ? context.text("connection_groups.port_missing")
          : context.text("connection_groups.port", ["port": String(value.port)])
        return proto + " · " + port
      }()

      let right = NSMutableAttributedString(
        string: protoPort + " · ",
        attributes: [.font: codeFont, .foregroundColor: PaxTheme.Color.textSecondary])
      right.append(
        NSAttributedString(
          string: String(value.connections),
          attributes: [.font: countFont, .foregroundColor: PaxTheme.Color.text]))
      let rightLabel = NSTextField(labelWithAttributedString: right)
      rightLabel.alignment = .right
      rightLabel.setContentHuggingPriority(.required, for: .horizontal)
      rightLabel.setContentCompressionResistancePriority(.required, for: .horizontal)

      let row = NSView()
      ipLabel.translatesAutoresizingMaskIntoConstraints = false
      rightLabel.translatesAutoresizingMaskIntoConstraints = false
      row.addSubview(ipLabel)
      row.addSubview(rightLabel)
      NSLayoutConstraint.activate([
        ipLabel.leadingAnchor.constraint(equalTo: row.leadingAnchor),
        ipLabel.topAnchor.constraint(equalTo: row.topAnchor),
        ipLabel.bottomAnchor.constraint(equalTo: row.bottomAnchor),
        ipLabel.trailingAnchor.constraint(
          lessThanOrEqualTo: rightLabel.leadingAnchor, constant: -8),
        rightLabel.trailingAnchor.constraint(equalTo: row.trailingAnchor),
        rightLabel.firstBaselineAnchor.constraint(equalTo: ipLabel.firstBaselineAnchor),
      ])
      return row
    }
    return WorkbenchConnectionReadoutLayout.insetBlock(
      title: context.text("connection_groups.address_protocol"), rows: rows, gap: 4)
  }

  private static func sourcesBlock(
    _ sources: [TrafficConnectionSource], context: PresentationContext
  ) -> NSView {
    let theme = PaxTheme.TrafficDetail.self
    let nameFont = PaxTheme.Typography.label(size: 12, weight: .medium)
    let kindFont = PaxTheme.Typography.label(size: theme.hoverMetaFont)
    let codeFont = NSFont.monospacedSystemFont(ofSize: 10.5, weight: .regular)
    let countFont = NSFont.monospacedSystemFont(ofSize: theme.hoverCodeFont, weight: .semibold)

    let rows = sources.map { value -> NSView in
      let name =
        value.application.isEmpty
        ? context.text("connection_groups.application_missing") : value.application
      let kind =
        value.kind == "tool"
        ? context.text("traffic_details.534e3e938c")
        : value.kind == "system" ? context.text("traffic_details.1f4b662752") : ""
      let titleAttr = NSMutableAttributedString(
        string: name, attributes: [.font: nameFont, .foregroundColor: PaxTheme.Color.text])
      if !kind.isEmpty {
        titleAttr.append(
          NSAttributedString(
            string: " (" + kind + ")",
            attributes: [.font: kindFont, .foregroundColor: PaxTheme.Color.textSecondary]))
      }
      let titleLabel = NSTextField(labelWithAttributedString: titleAttr)
      titleLabel.lineBreakMode = .byTruncatingTail

      var lines: [NSView] = [titleLabel]
      let process =
        value.process.isEmpty ? context.text("connection_groups.process_missing") : value.process
      let processLabel = PaxText.label(process, font: codeFont, secondary: true, wrapping: true)
      processLabel.lineBreakMode = .byCharWrapping
      lines.append(processLabel)
      if !value.applicationID.isEmpty {
        let bundleLabel = PaxText.label(
          value.applicationID, font: codeFont, secondary: true, wrapping: true)
        bundleLabel.lineBreakMode = .byTruncatingMiddle
        lines.append(bundleLabel)
      }

      let leftStack = WorkbenchConnectionReadoutLayout.stack(lines, gap: 2)
      let countLabel = PaxText.label(String(value.connections), font: countFont)
      countLabel.alignment = .right
      countLabel.setContentHuggingPriority(.required, for: .horizontal)
      countLabel.setContentCompressionResistancePriority(.required, for: .horizontal)

      let row = NSView()
      leftStack.translatesAutoresizingMaskIntoConstraints = false
      countLabel.translatesAutoresizingMaskIntoConstraints = false
      row.addSubview(leftStack)
      row.addSubview(countLabel)
      NSLayoutConstraint.activate([
        leftStack.leadingAnchor.constraint(equalTo: row.leadingAnchor),
        leftStack.topAnchor.constraint(equalTo: row.topAnchor),
        leftStack.bottomAnchor.constraint(equalTo: row.bottomAnchor),
        leftStack.trailingAnchor.constraint(equalTo: countLabel.leadingAnchor, constant: -10),
        countLabel.trailingAnchor.constraint(equalTo: row.trailingAnchor),
        countLabel.topAnchor.constraint(equalTo: row.topAnchor),
      ])
      return row
    }
    return WorkbenchConnectionReadoutLayout.insetBlock(
      title: context.text("connection_groups.process_identity"), rows: rows, gap: 7)
  }
}
