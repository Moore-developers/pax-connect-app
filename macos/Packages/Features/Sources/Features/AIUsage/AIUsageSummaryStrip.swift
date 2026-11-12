import AppKit
import PaxCore
import PaxUI

private let segmentIcon: CGFloat = 13
private let barHeight: CGFloat = 5
private let pillHeight: CGFloat = 18

/// Change pill text colours: green up, amber down, grey flat or no comparison.
func aiPillColor(_ tone: AIChangePill.Tone) -> NSColor {
  switch tone {
  case .up: PaxTheme.Color.healthy
  case .down: PaxTheme.Color.warning
  case .flat, .none: PaxTheme.Color.textSecondary
  }
}

/// Pill background: a light tint of the direction colour; amber uses the bright fill so it reads yellow.
func aiPillFill(_ tone: AIChangePill.Tone) -> NSColor {
  switch tone {
  case .up: PaxTheme.DashboardColor.green.withAlphaComponent(0.16)
  case .down: PaxTheme.DashboardColor.amber.withAlphaComponent(0.22)
  case .flat: PaxTheme.Color.textSecondary.withAlphaComponent(0.12)
  case .none: PaxTheme.Color.textSecondary.withAlphaComponent(0.08)
  }
}

/// Bar colours per card, matching the trend chart's input / output / cache layers.
func aiBarColors(_ kind: AIOverviewSegment.Kind) -> [NSColor] {
  switch kind {
  case .cost, .tokens:
    [PaxTheme.Chart.inputSeries, PaxTheme.Chart.outputSeries, PaxTheme.Chart.cacheSeries]
  case .requests: [PaxTheme.Color.healthy, PaxTheme.Color.error]
  case .cache: [PaxTheme.Chart.cacheSeries]
  }
}

/// Small rounded "↓59%" badge; the tooltip carries the previous value.
@MainActor
final class AIChangePillView: NSView {
  var pill: AIChangePill {
    didSet {
      toolTip = pill.tooltip
      setAccessibilityLabel(pill.text + " " + pill.tooltip)
      invalidateIntrinsicContentSize()
      needsDisplay = true
    }
  }
  /// The card's overlay button sits below the pill so its tooltip works; clicks still open the detail.
  var onClick: (() -> Void)?
  init(_ pill: AIChangePill) {
    self.pill = pill
    super.init(frame: .zero)
    toolTip = pill.tooltip.isEmpty ? nil : pill.tooltip
    setAccessibilityElement(true)
    setAccessibilityRole(.staticText)
    setAccessibilityLabel(pill.text + " " + pill.tooltip)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  private var attributes: [NSAttributedString.Key: Any] {
    [
      .font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.captionFont, weight: .semibold),
      .foregroundColor: aiPillColor(pill.tone),
    ]
  }
  override func mouseDown(with event: NSEvent) { onClick?() }
  override var isFlipped: Bool { true }
  override var intrinsicContentSize: NSSize {
    .init(
      width: (pill.text as NSString).size(withAttributes: attributes).width
        + PaxTheme.Spacing.medium,
      height: pillHeight)
  }
  override func draw(_ dirtyRect: NSRect) {
    let shape = NSBezierPath(
      roundedRect: bounds, xRadius: PaxTheme.Radius.control / 2,
      yRadius: PaxTheme.Radius.control / 2)
    aiPillFill(pill.tone).setFill()
    shape.fill()
    let size = (pill.text as NSString).size(withAttributes: attributes)
    (pill.text as NSString).draw(
      at: .init(x: (bounds.width - size.width) / 2, y: (bounds.height - size.height) / 2),
      withAttributes: attributes)
  }
  override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }
}

/// Thin segmented bar; a single segment (hit rate) sits on a neutral track.
@MainActor
final class AISegmentBar: NSView {
  var fractions: [Double] { didSet { needsDisplay = true } }
  let colors: [NSColor]
  init(fractions: [Double], colors: [NSColor]) {
    self.fractions = fractions
    self.colors = colors
    super.init(frame: .zero)
    setAccessibilityElement(false)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override var isFlipped: Bool { true }
  override var intrinsicContentSize: NSSize {
    .init(width: NSView.noIntrinsicMetric, height: barHeight)
  }
  override func draw(_ dirtyRect: NSRect) {
    let radius = barHeight / 2
    NSGraphicsContext.saveGraphicsState()
    NSBezierPath(roundedRect: bounds, xRadius: radius, yRadius: radius).addClip()
    PaxTheme.Color.cardBorder.setFill()
    bounds.fill()
    var x: CGFloat = 0
    for (index, fraction) in fractions.enumerated() {
      let width = bounds.width * CGFloat(max(0, min(1, fraction)))
      colors[index % colors.count].setFill()
      NSRect(x: x, y: 0, width: width, height: bounds.height).fill()
      x += width
    }
    NSGraphicsContext.restoreGraphicsState()
  }
  override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }
}

/// One statistic: title row with the change pill, value, thin segmented bar and legend line.
@MainActor
final class AIStatSegmentView: NSView {
  let pillView: AIChangePillView
  private var segment: AIOverviewSegment
  private let value = PaxText.label(
    "", font: PaxTheme.Typography.metric(size: PaxTheme.Dashboard.secondaryMetric))
  private let bar: AISegmentBar
  private let legend = AIUsageLayout.label(" ", secondary: true)
  init(_ segment: AIOverviewSegment) {
    self.segment = segment
    pillView = AIChangePillView(segment.pill)
    bar = AISegmentBar(fractions: segment.bar, colors: aiBarColors(segment.kind))
    super.init(frame: .zero)
    let icon = PaxIcon.view(segment.symbol, size: segmentIcon)
    let title = AIUsageLayout.label(segment.title, secondary: true)
    let heading = AIUsageLayout.stack([icon, title, NSView(), pillView], vertical: false)
    heading.spacing = PaxTheme.Spacing.tight
    pillView.setContentHuggingPriority(.required, for: .horizontal)
    pillView.setContentCompressionResistancePriority(.required, for: .horizontal)
    let text = NSMutableAttributedString(
      string: segment.value,
      attributes: [
        .font: PaxTheme.Typography.metric(size: PaxTheme.Dashboard.secondaryMetric),
        .foregroundColor: PaxTheme.Color.text,
      ])
    if !segment.unit.isEmpty {
      text.append(
        NSAttributedString(
          string: segment.unit,
          attributes: [
            .font: PaxTheme.Typography.label(size: PaxTheme.Control.compactUnit),
            .foregroundColor: PaxTheme.Color.textSecondary,
          ]))
    }
    value.attributedStringValue = text
    value.setContentCompressionResistancePriority(.required, for: .horizontal)
    // An empty legend keeps its height so the card does not jump between data and no data.
    legend.lineBreakMode = .byTruncatingTail
    legend.attributedStringValue = Self.legend(segment, named: true)
    legend.toolTip = segment.namedLegend.joined(separator: " / ")
    for view in [heading, value, bar, legend] {
      view.translatesAutoresizingMaskIntoConstraints = false
      addSubview(view)
    }
    NSLayoutConstraint.activate([
      heading.topAnchor.constraint(equalTo: topAnchor),
      heading.leadingAnchor.constraint(equalTo: leadingAnchor),
      heading.trailingAnchor.constraint(equalTo: trailingAnchor),
      heading.heightAnchor.constraint(equalToConstant: pillHeight),
      value.topAnchor.constraint(equalTo: heading.bottomAnchor, constant: PaxTheme.Spacing.tight),
      value.leadingAnchor.constraint(equalTo: leadingAnchor),
      bar.topAnchor.constraint(equalTo: value.bottomAnchor, constant: PaxTheme.Spacing.tight),
      bar.leadingAnchor.constraint(equalTo: leadingAnchor),
      bar.trailingAnchor.constraint(equalTo: trailingAnchor),
      bar.heightAnchor.constraint(equalToConstant: barHeight),
      legend.topAnchor.constraint(equalTo: bar.bottomAnchor, constant: PaxTheme.Spacing.tight),
      legend.leadingAnchor.constraint(equalTo: leadingAnchor),
      legend.trailingAnchor.constraint(equalTo: trailingAnchor),
      legend.bottomAnchor.constraint(equalTo: bottomAnchor),
    ])
    setAccessibilityElement(false)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  func apply(_ next: AIOverviewSegment) {
    guard segment != next else { return }
    segment = next
    pillView.pill = next.pill
    bar.fractions = next.bar
    let text = NSMutableAttributedString(
      string: segment.value,
      attributes: [
        .font: PaxTheme.Typography.metric(size: PaxTheme.Dashboard.secondaryMetric),
        .foregroundColor: PaxTheme.Color.text,
      ])
    if !segment.unit.isEmpty {
      text.append(
        NSAttributedString(
          string: segment.unit,
          attributes: [
            .font: PaxTheme.Typography.label(size: PaxTheme.Control.compactUnit),
            .foregroundColor: PaxTheme.Color.textSecondary,
          ]))
    }
    value.attributedStringValue = text
    legend.attributedStringValue = Self.legend(next, named: true)
    legend.toolTip = next.namedLegend.joined(separator: " / ")
    needsLayout = true
  }

  /// Narrow cards drop the "输入 / 输出 / 缓存" names and keep the dots; the tooltip still names them.
  override func layout() {
    super.layout()
    guard !segment.legendNames.isEmpty, legend.frame.width > 0 else { return }
    let named = Self.legend(segment, named: true)
    let fits = named.size().width + 4 <= legend.frame.width  // cell insets ~2pt per side
    let text = fits ? named : Self.legend(segment, named: false)
    if legend.attributedStringValue != text { legend.attributedStringValue = text }
  }

  /// Cost and Token legends tint a dot per bar segment; the others are plain secondary text.
  private static func legend(_ segment: AIOverviewSegment, named: Bool) -> NSAttributedString {
    let font = PaxTheme.Typography.label(size: PaxTheme.Dashboard.captionFont)
    let text = NSMutableAttributedString()
    let colors = aiBarColors(segment.kind)
    let dots = segment.kind == .cost || segment.kind == .tokens
    for (index, item) in (named ? segment.namedLegend : segment.legend).enumerated() {
      if index > 0 { text.append(.init(string: "  ", attributes: [.font: font])) }
      if dots {
        text.append(
          .init(string: "● ", attributes: [.font: font, .foregroundColor: colors[index]]))
      }
      text.append(
        .init(
          string: item, attributes: [.font: font, .foregroundColor: PaxTheme.Color.textSecondary]))
    }
    return segment.legend.isEmpty ? .init(string: " ", attributes: [.font: font]) : text
  }
}

extension AIUsageViewController {
  /// A single standard card with four equal segments and vertical dividers.
  func summaryStrip() -> NSView {
    var segments: [AIStatSegmentView] = []
    for segment in store.overviewSegments {
      let view = AIStatSegmentView(segment)
      let detail: AIUsageDetailController.Kind =
        segment.kind == .cost ? .cost : segment.kind == .requests ? .requests : .tokens
      let button = actions.overlay(
        label: "查看" + segment.title, id: segment.accessibilityID
      ) { [weak self, weak view] in self?.show(detail, anchor: view) }
      overlay(button, on: view, below: true)
      view.pillView.onClick = { [weak self, weak view] in self?.show(detail, anchor: view) }
      segments.append(view)
      contentRefreshers.append { [weak self, weak view] in
        guard let value = self?.store.overviewSegments.first(where: { $0.kind == segment.kind })
        else { return }
        view?.apply(value)
      }
    }
    return AIUsageLayout.segmentedCard(segments)
  }
}
