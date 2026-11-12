import AppKit
import PaxClient
import PaxCore
import PaxUI

/// One line of the limits list. Columns have fixed widths so the flexible bar column aligns; the
/// header in `AILimitListCard` uses the same widths. A 1px divider closes every row but the last.
@MainActor final class AILimitRowView: NSView, NSGestureRecognizerDelegate {
  static let iconWidth: CGFloat = 24
  static let nameWidth: CGFloat = 230
  static let multipleWidth: CGFloat = 76
  static let spendWidth: CGFloat = 96
  static let spacing = PaxTheme.Spacing.medium
  static let inset = PaxTheme.Spacing.tight
  private(set) var row: AILimitListRow
  private let readout: AILimitReadout
  var onSelect: ((String) -> Void)?
  var showsDivider = true {
    didSet { if showsDivider != oldValue { needsDisplay = true } }
  }
  private var hovered = false
  private let icon = NSImageView()
  private let name = PaxText.tableLabel("", identity: true)
  private let subtitle = PaxText.tableLabel("", secondary: true)
  private let multipleLabel = PaxText.tableLabel("", secondary: true, alignment: .center)
  private let spendLabel = PaxText.tableLabel(
    "", secondary: true, numeric: true, alignment: .center)
  private let band = AILimitBandBar(segments: [])
  private lazy var bandHost = AILimitReadoutHost(
    band, readout: readout,
    content: { [weak self] in
      self.map { AILimitRowReadout.view($0.row) } ?? NSView()
    })

  init(row: AILimitListRow, readout: AILimitReadout) {
    self.readout = readout
    self.row = row
    super.init(frame: .zero)
    // All row ink fits inside its padding. Bound descendants to this row so scrolling
    // does not traverse and repaint the entire expanded stack as one huge surface.
    clipsToBounds = true
    setAccessibilityElement(true)
    icon.imageScaling = .scaleProportionallyUpOrDown
    icon.widthAnchor.constraint(equalToConstant: Self.iconWidth).isActive = true
    icon.heightAnchor.constraint(equalToConstant: Self.iconWidth).isActive = true
    icon.setAccessibilityElement(false)
    let stack = AIUsageLayout.stack(
      [icon, names(), bar(), multiple(), spend()], vertical: false)
    stack.spacing = Self.spacing
    stack.distribution = .fill
    stack.translatesAutoresizingMaskIntoConstraints = false
    addSubview(stack)
    let vertical = PaxTheme.Spacing.medium
    NSLayoutConstraint.activate([
      stack.leadingAnchor.constraint(equalTo: leadingAnchor, constant: Self.inset),
      stack.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -Self.inset),
      stack.topAnchor.constraint(equalTo: topAnchor, constant: vertical),
      stack.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -vertical),
    ])
    let click = NSClickGestureRecognizer(target: self, action: #selector(clicked(_:)))
    click.delegate = self
    addGestureRecognizer(click)
    addTrackingArea(
      NSTrackingArea(
        rect: .zero, options: [.mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect],
        owner: self))
    updateContent(previous: nil)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  private func fixed(_ label: NSTextField, _ width: CGFloat) -> NSTextField {
    label.widthAnchor.constraint(equalToConstant: width).isActive = true
    return label
  }

  /// Name, then a small second line: model, effort as plain small text, and the note.
  private func names() -> NSView {
    name.lineBreakMode = .byTruncatingTail
    subtitle.lineBreakMode = .byTruncatingTail
    let column = AIUsageLayout.stack([name, subtitle])
    column.spacing = PaxTheme.AISession.contextLineSpacing
    column.widthAnchor.constraint(equalToConstant: Self.nameWidth).isActive = true
    let height =
      name.intrinsicContentSize.height + subtitle.intrinsicContentSize.height + column.spacing
    return AIUsageLayout.wrap(column, width: Self.nameWidth, height: height)
  }

  private func bar() -> NSView {
    bandHost.setAccessibilityElement(true)
    bandHost.setAccessibilityRole(.button)
    bandHost.setContentHuggingPriority(.init(1), for: .horizontal)
    bandHost.setContentCompressionResistancePriority(.init(1), for: .horizontal)
    return bandHost
  }

  func apply(_ row: AILimitListRow) {
    guard self.row != row else { return }
    let previous = self.row
    self.row = row
    updateContent(previous: previous)
  }

  private func updateContent(previous: AILimitListRow?) {
    if previous?.cli != row.cli {
      icon.image = AIClient(code: row.cli).flatMap { AIClientMark.image($0) }
    }
    if name.stringValue != row.title {
      name.stringValue = row.title
      name.toolTip = row.title
    }
    if previous?.emphasized != row.emphasized {
      name.font = PaxTheme.Typography.label(
        size: PaxTheme.Routes.nameFont, weight: row.emphasized ? .bold : .medium)
    }
    var parts: [String] = []
    if !row.model.isEmpty && row.title != row.model { parts.append(row.model) }
    if !row.effort.isEmpty { parts.append(row.effort) }
    if !row.note.isEmpty { parts.append(row.note) }
    let detail = parts.joined(separator: " · ")
    subtitle.isHidden = detail.isEmpty
    if subtitle.stringValue != detail {
      subtitle.stringValue = detail
      subtitle.toolTip = detail
    }
    setAccessibilityLabel(row.title)
    setAccessibilityValue(valueText())
    setAccessibilityRole(row.sessionID == nil ? .group : .button)
    if row.sessionID == nil, hovered {
      hovered = false
      needsDisplay = true
    }
    bandHost.setAccessibilityIdentifier("ai-session-limit-band-" + row.id)
    bandHost.setAccessibilityLabel(barTip())
    let segments: [AILimitBandBar.Segment]
    if row.lines == nil {
      segments = [
        .init(
          share: row.input.official > 0 ? 100 : 0, text: "未设上限 100%",
          fill: AILimitStyle.neutralFill, tone: AILimitStyle.neutral)
      ]
    } else {
      segments = (0..<3).map {
        .init(
          share: row.percents[$0], text: "\(row.percents[$0])%",
          fill: AILimitStyle.bandFills[$0], tone: AILimitStyle.bandLabels[$0], striped: $0 == 2)
      }
    }
    band.apply(segments: segments)
    let multipleText = AILimitStyle.multipleText(row.multiple)
    if multipleLabel.stringValue != multipleText { multipleLabel.stringValue = multipleText }
    multipleLabel.font = PaxTheme.Typography.time
    multipleLabel.textColor = PaxTheme.Table.muted
    if case .value(let value) = row.multiple, judged {
      if value >= 2 {
        multipleLabel.textColor = PaxTheme.DashboardColor.redLabel
        multipleLabel.font = .monospacedDigitSystemFont(ofSize: 12, weight: .semibold)
      }
    } else {
      multipleLabel.textColor = AILimitStyle.dim
    }
    let value = judged ? row.overSpend?.official : nil
    let spendText = AILimitStyle.plus(value)
    if spendLabel.stringValue != spendText { spendLabel.stringValue = spendText }
    spendLabel.textColor = value == nil ? AILimitStyle.dim : PaxTheme.Table.muted
  }

  /// Without an over-limit request there is nothing to multiply: a grey dash.
  private var judged: Bool { row.bands[2].requests > 0 }

  private func multiple() -> NSView {
    let label = fixed(multipleLabel, Self.multipleWidth)
    label.toolTip = nil
    return AILimitReadoutHost(label, readout: readout) { [weak self] in
      AILimitReadout.explanation("费用倍率", self?.multipleTip() ?? "")
    }
  }

  private func spend() -> NSView {
    let label = fixed(spendLabel, Self.spendWidth)
    label.toolTip = nil
    return AILimitReadoutHost(label, readout: readout) { [weak self] in
      AILimitReadout.explanation("超限增费", self?.spendTip() ?? "")
    }
  }

  private func valueText() -> String {
    "费用倍率 " + (judged ? AILimitStyle.multipleText(row.multiple) : "—") + "，超限增费 "
      + (judged ? AILimitStyle.plus(row.overSpend?.official) : "—")
  }

  // MARK: Hover text

  private func barTip() -> String {
    var lines: [String] = []
    if row.lines == nil {
      lines.append(
        "该模型未设上限，整条为一段：" + AILimitStyle.both(row.input, precision: 4) + " · \(row.requests) 次")
    } else {
      let edges = AILimitStyle.edges(row.lines)
      for index in 0..<3 {
        let band = row.bands[index]
        let name = "\(AILimitStyle.bandNames[index]) \(edges[index])："
        lines.append(
          name + AILimitStyle.both(band.money, precision: 4)
            + " · \(band.requests) 次 · \(row.percents[index])%")
      }
    }
    if row.unpriced > 0 { lines.append("另有 \(row.unpriced) 次未定价请求，不计入费用与占比") }
    if let flat = row.flat {
      lines.append(
        "不翻倍情形（上限 \(AILimitStyle.k(flat.limit))）：>\(AILimitStyle.k(flat.limit)) 共 \(flat.requests) 次，"
          + AILimitStyle.both(flat.cost, precision: 4))
    }
    lines.append("百分比 = 各段输入费用 ÷ 本行输入费用（官方口径）；超限段为斜纹")
    if row.modelLines.count > 1 {
      lines.append("各模型的线：")
      for item in row.modelLines {
        lines.append(
          "  " + item.model + " "
            + AILimitStyle.edges(item.limit).joined(separator: " / ") + " · \(item.requests) 次")
      }
    }
    return lines.joined(separator: "\n")
  }

  private func multipleTip() -> String {
    guard judged else { return "没有超限请求，无需比较。" }
    guard row.multiple != .dash else { return "缺少可比较的费用，暂时算不出。" }
    var text = AILimitStyle.multipleExplanation
    if row.bands[0].pricedRequests == 0 {
      text += "\n没有已定价安全记录时，按模型参考费用比较。"
    }
    return text
  }

  private func spendTip() -> String {
    guard judged else { return "没有超限请求，没有额外费用。" }
    guard let spend = row.overSpend else { return "缺少可比较的费用，暂时算不出。" }
    var text = AILimitStyle.spendExplanation
    if spend.official < 0 { text += "\n负数表示花费比参考费用低。" }
    return text
  }

  // MARK: Interaction

  override func accessibilityPerformPress() -> Bool {
    guard let id = row.sessionID else { return false }
    onSelect?(id)
    return true
  }
  func gestureRecognizerShouldBegin(_ gestureRecognizer: NSGestureRecognizer) -> Bool {
    !bandHost.bounds.contains(gestureRecognizer.location(in: bandHost))
  }
  @objc private func clicked(_ gesture: NSClickGestureRecognizer) {
    let point = gesture.location(in: bandHost)
    guard !bandHost.bounds.contains(point) else { return }
    if let id = row.sessionID { onSelect?(id) }
  }
  override func mouseEntered(with event: NSEvent) {
    guard row.sessionID != nil else { return }
    hovered = true
    needsDisplay = true
  }
  override func mouseExited(with event: NSEvent) {
    hovered = false
    needsDisplay = true
  }
  override func draw(_ dirtyRect: NSRect) {
    guard bounds.intersects(visibleRect) else { return }
    if hovered {
      PaxTheme.Color.navigationHover.withAlphaComponent(0.7).setFill()
      NSBezierPath(
        roundedRect: bounds, xRadius: PaxTheme.Radius.control, yRadius: PaxTheme.Radius.control
      ).fill()
    }
    guard showsDivider else { return }
    PaxTheme.DashboardColor.divider.setFill()
    NSRect(x: 0, y: 0, width: bounds.width, height: PaxTheme.Layout.cardBorder).fill()
  }
}
