import AppKit

/// A threshold ruler and two direction bands from the same frozen route snapshot.
public final class PaxRouteAlertReadout: NSStackView {
  public struct DirectionValue {
    public let balance, detail: String
    public let usedFraction: Double?
    public let reached: Bool
    public init(balance: String, detail: String, usedFraction: Double?, reached: Bool) {
      self.balance = balance
      self.detail = detail
      self.usedFraction = usedFraction
      self.reached = reached
    }
  }
  public struct Period {
    public let title, detailCaption, help: String
    public let upload, download: DirectionValue
    public init(
      title: String, detailCaption: String, help: String, upload: DirectionValue,
      download: DirectionValue
    ) {
      self.title = title
      self.detailCaption = detailCaption
      self.help = help
      self.upload = upload
      self.download = download
    }
  }
  public struct Focus {
    public let title, prefix, amount, used, threshold: String
    public let fraction: Double
    public let upload, download, help: String
    public let reached: Bool
    public init(
      title: String, prefix: String, amount: String, used: String, threshold: String,
      fraction: Double, upload: String, download: String, help: String,
      reached: Bool
    ) {
      self.title = title
      self.prefix = prefix
      self.amount = amount
      self.used = used
      self.threshold = threshold
      self.fraction = fraction
      self.upload = upload
      self.download = download
      self.help = help
      self.reached = reached
    }
  }

  private typealias Theme = PaxTheme.UsageReadout
  private let tableTitle, direction, detailHint, used, upload, download: String
  public var onResize: (() -> Void)?
  public init(
    comparison: String, direction: String, detailHint: String, used: String, upload: String,
    download: String
  ) {
    tableTitle = comparison
    self.direction = direction
    self.detailHint = detailHint
    self.used = used
    self.upload = "↑ " + upload
    self.download = "↓ " + download
    super.init(frame: .zero)
    orientation = .vertical
    alignment = .leading
    spacing = PaxTheme.Spacing.tight
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }

  public func apply(focus: Focus?, periods: [Period], note: String) {
    for child in arrangedSubviews { child.removeFromSuperview() }
    if !note.isEmpty { addLine(caption(note)) }
    if let focus { addFocus(focus) }
    if !periods.isEmpty {
      let separator = NSBox()
      separator.boxType = .separator
      addLine(separator)
      let title = PaxText.label(
        tableTitle,
        font: PaxTheme.Typography.label(size: PaxTheme.Routes.nameFont, weight: .semibold))
      addLine(row([title, NSView(), caption(detailHint)]))
      let headings = periods.map { period in
        let field = caption(period.title)
        field.alignment = .right
        field.toolTip = period.help
        return field
      }
      addLine(bandRow(stub: caption(direction), cells: headings, fill: nil))
      addLine(directionBand(periods, up: true))
      addLine(directionBand(periods, up: false))
    }
    onResize?()
  }

  private func addFocus(_ focus: Focus) {
    let up = caption(upload + " " + focus.upload)
    let down = caption(download + " " + focus.download)
    up.textColor = Theme.uploadInk
    down.textColor = Theme.downloadInk
    let title = PaxText.label(focus.title, font: PaxTheme.Typography.caption())
    let header = row([title, NSView(), caption(used), up, down])
    header.toolTip = focus.help
    addLine(header)

    let metricInk = focus.reached ? PaxTheme.Color.error : PaxTheme.Color.text
    let text = NSMutableAttributedString(
      string: focus.amount,
      attributes: [
        .font: PaxTheme.Typography.metric(size: Theme.mainNumber, weight: .semibold),
        .foregroundColor: metricInk,
      ])
    if !focus.prefix.isEmpty, let space = focus.amount.lastIndex(of: " ") {
      text.addAttribute(
        .font, value: PaxTheme.Typography.tableValue,
        range: NSRange(space..<focus.amount.endIndex, in: focus.amount))
    }
    let amount = NSTextField(labelWithAttributedString: text)
    let prefix = caption(focus.prefix)
    if focus.reached { prefix.textColor = metricInk }
    let metric = row(focus.prefix.isEmpty ? [amount, NSView()] : [prefix, amount, NSView()])
    metric.alignment = .firstBaseline
    addLine(metric)
    let ruler = ThresholdRuler(fraction: focus.fraction)
    addLine(ruler)
    setCustomSpacing(PaxTheme.Spacing.xs, after: ruler)
    addLine(row([caption(focus.used), NSView(), caption(focus.threshold)]))
  }

  private func directionBand(_ periods: [Period], up: Bool) -> NSView {
    let name = up ? upload : download
    let stub = PaxText.label(
      name, font: PaxTheme.Typography.label(size: PaxTheme.Control.compactUnit, weight: .medium))
    stub.textColor = up ? Theme.uploadInk : Theme.downloadInk
    let cells = periods.map { period in
      let value = up ? period.upload : period.download
      let balance = PaxText.label(
        value.balance,
        font: PaxTheme.Typography.metric(size: Theme.bandValueSize, weight: .semibold))
      if value.reached { balance.textColor = PaxTheme.Color.error }
      let detail = caption(value.detail)
      let context = period.title + " · " + name
      balance.setAccessibilityLabel(context + " · " + value.balance)
      detail.setAccessibilityLabel(context + " · " + period.detailCaption + " · " + value.detail)
      balance.alignment = .right
      detail.alignment = .right
      let stack = NSStackView(views: [balance, detail])
      stack.orientation = .vertical
      stack.alignment = .trailing
      stack.spacing = PaxTheme.Spacing.xs / 2
      stack.toolTip = [
        context, value.balance, period.detailCaption + "  " + value.detail, period.help,
      ]
      .joined(separator: "\n")
      return stack
    }
    return bandRow(stub: stub, cells: cells, fill: up ? Theme.uploadBand : Theme.downloadBand)
  }

  private func bandRow(stub: NSView, cells: [NSView], fill: NSColor?) -> NSView {
    let stack = PeriodBand(views: [stub] + cells)
    stack.fill = fill
    stack.distribution = .fill
    stack.spacing = PaxTheme.Spacing.xs
    stack.alignment = .centerY
    let inset = PaxTheme.Spacing.tight
    let vertical = fill == nil ? 0 : inset
    stack.edgeInsets = .init(top: vertical, left: inset, bottom: vertical, right: inset)
    stub.widthAnchor.constraint(equalToConstant: Theme.directionWidth).isActive = true
    if let first = cells.first {
      for cell in cells.dropFirst() {
        cell.widthAnchor.constraint(equalTo: first.widthAnchor).isActive = true
      }
    }
    return stack
  }

  private func caption(_ text: String) -> NSTextField {
    let field = PaxText.label(text, font: PaxTheme.Typography.label(size: Theme.detailSize))
    field.textColor = Theme.detailInk
    return field
  }
  private func row(_ views: [NSView]) -> NSStackView {
    let stack = NSStackView(views: views)
    stack.alignment = .centerY
    stack.spacing = PaxTheme.Spacing.tight
    return stack
  }
  private func addLine(_ view: NSView) {
    addArrangedSubview(view)
    view.widthAnchor.constraint(equalTo: widthAnchor).isActive = true
  }
}

private final class PeriodBand: NSStackView {
  var fill: NSColor?
  override func draw(_ dirtyRect: NSRect) {
    if let fill {
      fill.setFill()
      let radius = PaxTheme.Radius.control
      NSBezierPath(roundedRect: bounds, xRadius: radius, yRadius: radius).fill()
    }
    super.draw(dirtyRect)
  }
}

/// The pin saturates at the limit; the text beside it retains the actual usage.
private final class ThresholdRuler: NSView {
  let fraction: CGFloat
  private var ink: NSColor {
    switch fraction {
    case ..<0.25: PaxTheme.DashboardColor.green
    case ..<0.75: PaxTheme.DashboardColor.amber
    default: PaxTheme.DashboardColor.red
    }
  }
  init(fraction: Double) {
    self.fraction = min(max(fraction, 0), 1)
    super.init(frame: .zero)
    heightAnchor.constraint(equalToConstant: PaxTheme.UsageReadout.rulerHeight).isActive = true
    setAccessibilityElement(false)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  override func draw(_ dirtyRect: NSRect) {
    let thickness = PaxTheme.Spacing.xs
    let hairline = PaxTheme.Layout.cardBorder
    let track = bounds.insetBy(dx: thickness / 2, dy: (bounds.height - thickness) / 2)
    func pill(_ rect: NSRect, color: NSColor) {
      color.setFill()
      NSBezierPath(roundedRect: rect, xRadius: thickness / 2, yRadius: thickness / 2).fill()
    }
    pill(track, color: PaxTheme.Color.cardBorder)
    let usedWidth = track.width * fraction
    if usedWidth > 0 {
      pill(
        NSRect(origin: track.origin, size: .init(width: usedWidth, height: thickness)), color: ink)
    }
    for index in 0...4 {
      let x = track.minX + track.width * CGFloat(index) / 4
      let height = index == 4 ? bounds.height : PaxTheme.Spacing.tight
      PaxTheme.Color.textSecondary.setFill()
      NSRect(x: x - hairline / 2, y: bounds.midY - height / 2, width: hairline, height: height)
        .fill()
    }
    let pin = NSRect(
      x: track.minX + usedWidth - thickness / 2, y: bounds.minY + thickness / 2,
      width: thickness, height: bounds.height - thickness)
    pill(pin.insetBy(dx: -hairline, dy: -hairline), color: PaxTheme.UsageReadout.surface)
    pill(pin, color: ink)
  }
}
