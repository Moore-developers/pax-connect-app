import AppKit
import PaxCore
import PaxUI

/// Header, metric/evidence body, and a full-width footer share three stable baselines.
@MainActor final class AILimitKpiCard: NSView {
  enum EvidenceLayout { case input, means, activity }
  static let height: CGFloat = 156
  let title = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private let context = PaxText.label(
    "", font: PaxTheme.Typography.label(size: 11), secondary: true)
  private let value = PaxText.label("", font: PaxTheme.Typography.metric(size: 28, weight: .light))
  private let sub = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private let evidenceHost = NSView()
  private let footerHost = NSView()

  init(evidenceWidth: CGFloat? = nil, evidenceLayout: EvidenceLayout = .activity) {
    super.init(frame: .zero)
    let rule = AIUsageLayout.separator()
    let readout = NSLayoutGuide()
    addLayoutGuide(readout)
    for view in [title, context, value, sub, evidenceHost, rule, footerHost] {
      view.translatesAutoresizingMaskIntoConstraints = false
      addSubview(view)
    }
    sub.lineBreakMode = .byTruncatingTail
    sub.maximumNumberOfLines = 1
    NSLayoutConstraint.activate([
      heightAnchor.constraint(equalToConstant: Self.height - PaxTheme.Spacing.card * 2),
      title.topAnchor.constraint(equalTo: topAnchor),
      title.leadingAnchor.constraint(equalTo: leadingAnchor),
      context.trailingAnchor.constraint(equalTo: trailingAnchor),
      context.centerYAnchor.constraint(equalTo: title.centerYAnchor),
      value.topAnchor.constraint(
        equalTo: title.bottomAnchor, constant: evidenceLayout == .means ? 15 : 18),
      value.leadingAnchor.constraint(equalTo: leadingAnchor),
      value.trailingAnchor.constraint(lessThanOrEqualTo: evidenceHost.leadingAnchor, constant: -8),
      sub.topAnchor.constraint(equalTo: value.bottomAnchor, constant: PaxTheme.Spacing.tight),
      sub.leadingAnchor.constraint(equalTo: leadingAnchor),
      sub.trailingAnchor.constraint(equalTo: evidenceHost.leadingAnchor, constant: -8),
      evidenceHost.trailingAnchor.constraint(equalTo: trailingAnchor),
      readout.topAnchor.constraint(equalTo: title.topAnchor),
      readout.bottomAnchor.constraint(equalTo: sub.bottomAnchor),
      evidenceHost.heightAnchor.constraint(equalToConstant: 80),
      rule.leadingAnchor.constraint(equalTo: leadingAnchor),
      rule.trailingAnchor.constraint(equalTo: trailingAnchor),
      rule.bottomAnchor.constraint(equalTo: footerHost.topAnchor, constant: -10),
      footerHost.heightAnchor.constraint(equalToConstant: 16),
      footerHost.leadingAnchor.constraint(equalTo: leadingAnchor),
      footerHost.trailingAnchor.constraint(equalTo: trailingAnchor),
      footerHost.bottomAnchor.constraint(equalTo: bottomAnchor),
    ])
    let evidencePosition: NSLayoutConstraint
    switch evidenceLayout {
    case .input:
      evidencePosition = evidenceHost.centerYAnchor.constraint(equalTo: readout.centerYAnchor)
    case .means:
      // Keep the former 176pt body's center, independent of text metrics, then lift 11pt.
      let originalBody = NSLayoutGuide()
      addLayoutGuide(originalBody)
      NSLayoutConstraint.activate([
        originalBody.topAnchor.constraint(equalTo: title.bottomAnchor, constant: 10),
        originalBody.bottomAnchor.constraint(equalTo: rule.topAnchor, constant: 10),
      ])
      evidencePosition = evidenceHost.centerYAnchor.constraint(
        equalTo: originalBody.centerYAnchor, constant: -11)
    case .activity:
      evidencePosition = evidenceHost.bottomAnchor.constraint(equalTo: rule.topAnchor)
    }
    evidencePosition.isActive = true
    if let evidenceWidth {
      evidenceHost.widthAnchor.constraint(equalToConstant: evidenceWidth).isActive = true
    } else {
      evidenceHost.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 92).isActive = true
    }
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  func apply(
    title: String, value: String, unit: String = "", sub: String,
    context: String = "", color: NSColor? = nil, subColor: NSColor? = nil,
    evidence: NSView, footer: NSView
  ) {
    self.title.stringValue = title
    self.context.stringValue = context
    let text = NSMutableAttributedString(
      string: value,
      attributes: [
        .font: PaxTheme.Typography.metric(size: 28, weight: .light),
        .foregroundColor: color ?? PaxTheme.Color.text,
      ])
    text.append(
      NSAttributedString(
        string: unit,
        attributes: [
          .font: PaxTheme.Typography.metric(size: 14, weight: .light),
          .foregroundColor: PaxTheme.Color.textSecondary,
        ]))
    self.value.attributedStringValue = text
    self.value.toolTip = value + unit
    self.sub.stringValue = sub
    self.sub.textColor = subColor ?? PaxTheme.Color.textSecondary
    place(evidence, in: evidenceHost)
    place(footer, in: footerHost)
    setAccessibilityLabel(title)
  }

  private func place(_ content: NSView, in host: NSView) {
    for child in host.subviews { child.removeFromSuperview() }
    host.addSubview(content)
    content.translatesAutoresizingMaskIntoConstraints = false
    NSLayoutConstraint.activate([
      content.leadingAnchor.constraint(equalTo: host.leadingAnchor),
      content.trailingAnchor.constraint(equalTo: host.trailingAnchor),
      content.centerYAnchor.constraint(equalTo: host.centerYAnchor),
      content.topAnchor.constraint(greaterThanOrEqualTo: host.topAnchor),
      content.bottomAnchor.constraint(lessThanOrEqualTo: host.bottomAnchor),
    ])
  }
}

/// Input/estimated excess, observed average ratio, and activity/current context.
@MainActor final class AILimitSummaryStrip {
  let interfaceCopy: InterfaceCopyBindings
  var context: PresentationContext { interfaceCopy.context }

  let view: NSView
  private let cards = [
    AILimitKpiCard(evidenceWidth: 78, evidenceLayout: .input),
    AILimitKpiCard(evidenceWidth: 112, evidenceLayout: .means), AILimitKpiCard(),
  ]
  let readout: AILimitReadout
  private let activityHost = NSView()

  init(interfaceCopy: InterfaceCopyBindings = InterfaceCopyBindings()) {
    self.interfaceCopy = interfaceCopy
    let context = interfaceCopy.context

    readout = AILimitReadout(interfaceCopy: interfaceCopy)
    let surfaces =
      cards.prefix(2).map { AIUsageLayout.card($0) } + [AIUsageLayout.card(activityHost)]
    let row = NSStackView(views: surfaces)
    row.orientation = .horizontal
    row.distribution = .fill
    row.spacing = PaxTheme.Spacing.regular
    NSLayoutConstraint.activate([
      surfaces[0].widthAnchor.constraint(equalTo: surfaces[1].widthAnchor, constant: 24),
      surfaces[2].widthAnchor.constraint(equalTo: surfaces[1].widthAnchor, multiplier: 1.25),
    ])
    for index in 0..<2 {
      let help = readout.help { context in
        (
          context.text(index == 0 ? "session_limits.d00bc9b5f7" : "session_limits.76c84b1e3a"),
          index == 0
            ? context.text("session_limits.b74590e63f")
              + AILimitStyle.spendExplanation(context: context)
            : context.text("session_limits.5a8f5be2f8")
        )
      }
      cards[index].addSubview(help)
      help.translatesAutoresizingMaskIntoConstraints = false
      NSLayoutConstraint.activate([
        help.leadingAnchor.constraint(
          equalTo: cards[index].title.trailingAnchor, constant: PaxTheme.Spacing.tight),
        help.centerYAnchor.constraint(equalTo: cards[index].title.centerYAnchor),
      ])
    }
    row.setAccessibilityIdentifier("ai-session-limits-summary")
    view = row
  }

  private func bar(_ fraction: Double, color: NSColor) -> NSView {
    let result = PaxShareBar(
      segments: [.init(context.text("session_limits.cc87b27b64"), value: min(1, max(0, fraction)))],
      colors: [color], total: 1)
    result.heightAnchor.constraint(equalToConstant: 5).isActive = true
    return result
  }

  static func change(_ value: Double, from base: Double?, context: PresentationContext = .chinese)
    -> String?
  {
    guard let base, base > 0 else { return nil }
    let delta = (value / base - 1) * 100
    if abs(delta) < 0.05 { return context.text("session_limits.557a26fc6a") }
    let number =
      abs(delta) < 1 ? String(format: "%.1f", abs(delta)) : String(format: "%.0f", abs(delta))
    return context.text("session_limits.61e91c81b3") + (delta > 0 ? "↑" : "↓") + number + "%"
  }

  private func footer(_ label: String, value: String, color: NSColor? = nil) -> NSView {
    let text = PaxText.label(label, font: PaxTheme.Typography.caption(), secondary: true)
    let amount = PaxText.label(value, font: PaxTheme.Typography.time)
    amount.textColor = color ?? PaxTheme.Color.textSecondary
    let row = AIUsageLayout.stack([text, NSView(), amount], vertical: false)
    row.spacing = PaxTheme.Spacing.xs
    return row
  }

  private func costEvidence(_ summary: AILimitSummary) -> NSView {
    let spend = summary.overSpend.official
    let share = summary.input.official > 0 && spend >= 0 ? spend / summary.input.official : nil
    let fraction = min(1, max(0, share ?? 0))
    let ring = PaxDonutChart(
      segments: [
        .init(context.text("session_limits.8dad12266e"), value: fraction),
        .init(context.text("session_limits.ab6e07d6e8"), value: 1 - fraction),
      ],
      center: AIUsageFormatting.percent(share), compact: true,
      colors: [PaxTheme.DashboardColor.red, PaxTheme.Color.cardBorder],
      centerFont: PaxTheme.Typography.label(size: 12))
    ring.setAccessibilityLabel(
      context.text("session_limits.09feb8e761") + AIUsageFormatting.percent(share))
    ring.heightAnchor.constraint(equalToConstant: 78).isActive = true
    return ring
  }

  private func means(_ summary: AILimitSummary) -> NSView {
    let safe = summary.bands[0].average?.official
    let over = summary.bands[2].average?.official
    let ceiling = max(safe ?? 0, over ?? 0)
    let rows = [
      (context.text("session_limits.afb63a620b"), safe, AILimitStyle.bandColors[0]),
      (context.text("session_limits.314600a8ae"), over, AILimitStyle.bandColors[2]),
    ]
    .map {
      name, amount, color in
      let label = PaxText.label(name, font: PaxTheme.Typography.caption(), secondary: true)
      let money = PaxText.label(
        AILimitStyle.money(amount), font: PaxTheme.Typography.time
      )
      label.font = PaxTheme.Typography.label(size: 11)
      money.font = PaxTheme.Typography.label(size: 11)
      let heading = AIUsageLayout.stack([label, NSView(), money], vertical: false)
      heading.spacing = 3
      let row = AIUsageLayout.stack([
        heading, bar(ceiling > 0 ? (amount ?? 0) / ceiling : 0, color: color),
      ])
      row.spacing = 3
      return row
    }
    let body = AIUsageLayout.stack(rows)
    body.spacing = PaxTheme.Spacing.medium
    return body
  }

  func apply(_ summary: AILimitSummary) {
    let previous = summary.comparison
    let costChange =
      summary.days.count == 1
      ? Self.change(summary.input.official, from: previous?.input.official, context: context) : nil
    let spend = summary.overSpend.official
    cards[0].apply(
      title: context.text("session_limits.fe4606df06"),
      value: AILimitStyle.money(summary.input.official, precision: 2),
      sub: summary.unpriced > 0
        ? context.text("session_limits.50b58e4cfa", ["arg0": String(describing: summary.unpriced)])
        : costChange ?? context.text("session_limits.da092271da"),
      subColor: costChange != nil && summary.input.official < (previous?.input.official ?? 0)
        ? PaxTheme.DashboardColor.greenLabel : nil,
      evidence: costEvidence(summary),
      footer: footer(
        context.text("session_limits.8dad12266e"), value: AILimitStyle.plus(spend),
        color: spend > 0 ? PaxTheme.DashboardColor.redLabel : nil))
    cards[1].apply(
      title: context.text("session_limits.76c84b1e3a"),
      value: AILimitStyle.multipleText(summary.multiple),
      sub: context.text("session_limits.f54977dbae"), evidence: means(summary),
      footer: footer(
        context.text("session_limits.fe27ff0358"), value: context.text("session_limits.c7f649399a"))
    )
    if let current = summary.current {
      let limit = current.limit
      let tone = limit == nil ? nil : AILimitStyle.bandLabels[current.band]
      let capacity = current.nativeContextLimit.map {
        context.text("session_limits.native_context_limit", ["tokens": AILimitStyle.k($0)])
      }
      let gauge = AIUsageLayout.stack([
        PaxText.label(
          limit.map { context.text("session_limits.0c69edbc82") + AILimitStyle.k($0) }
            ?? capacity ?? context.text("session_limits.0c2315db3a"),
          font: PaxTheme.Typography.caption(), secondary: true),
        bar(
          (limit ?? current.nativeContextLimit).map { Double(current.context) / Double($0) } ?? 0,
          color: tone ?? AILimitStyle.neutral),
      ])
      let status: String
      if let limit, current.context > limit {
        status = context.text("session_limits.b75bde28d7") + AILimitStyle.k(current.context - limit)
      } else if limit != nil {
        status = AILimitStyle.bandNames(context: context)[current.band]
      } else if let nativeLimit = current.nativeContextLimit {
        status = AIUsageFormatting.percent(Double(current.context) / Double(nativeLimit))
      } else {
        status = context.text("session_limits.0c2315db3a")
      }
      cards[2].apply(
        title: context.text("session_limits.080bc981ec"), value: AILimitStyle.k(current.context),
        sub: status,
        color: tone, subColor: tone, evidence: gauge,
        footer: footer(
          context.text("session_limits.8b01b9b98b"),
          value: limit == nil ? "" : capacity ?? ""))
      showActivity(cards[2])
    } else {
      showActivity(
        AILimitActivityView(summary: summary, readout: readout, interfaceCopy: interfaceCopy))
    }
  }

  private func showActivity(_ content: NSView) {
    for child in activityHost.subviews { child.removeFromSuperview() }
    activityHost.addSubview(content)
    content.translatesAutoresizingMaskIntoConstraints = false
    NSLayoutConstraint.activate([
      content.leadingAnchor.constraint(equalTo: activityHost.leadingAnchor),
      content.trailingAnchor.constraint(equalTo: activityHost.trailingAnchor),
      content.topAnchor.constraint(equalTo: activityHost.topAnchor),
      content.bottomAnchor.constraint(equalTo: activityHost.bottomAnchor),
    ])
  }
}
