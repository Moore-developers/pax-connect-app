import AppKit

/// 原稿指标组合：单位为次级字体，小图紧邻数值右侧，时间在同一基线下方。
public final class PaxRouteMetric: NSStackView {
  public enum Style { case test, usage }
  private let valueField = NSTextField(labelWithString: "")
  private var valueAttributes: [NSAttributedString.Key: Any] = [:]
  private var metricTitle = ""
  private var metricCaption = ""
  public func updateValue(_ value: String, unit: String = "") {
    let text = NSMutableAttributedString(string: value, attributes: valueAttributes)
    if !unit.isEmpty {
      text.append(
        NSAttributedString(
          string: " " + unit,
          attributes: [
            .font: PaxTheme.Typography.caption(), .foregroundColor: PaxTheme.Routes.muted,
          ]))
    }
    valueField.attributedStringValue = text
    setAccessibilityLabel(
      [metricTitle, value, unit, metricCaption].filter { !$0.isEmpty }.joined(separator: "，"))
  }
  public init(
    title: String, symbol: String, value: String, unit: String = "", caption: String = "",
    samples: [Double] = [], color: NSColor, failed: Bool = false, bars: Bool = false,
    style: Style = .test, valueColor: NSColor? = nil
  ) {
    super.init(frame: .zero)
    orientation = .vertical
    alignment = .leading
    spacing = PaxTheme.Routes.metricGap
    let icon = PaxRouteIcon.view(symbol, size: PaxTheme.Routes.nameFont)
    icon.contentTintColor = color
    let heading = NSStackView(views: [
      icon, PaxRouteText.label(title, font: PaxTheme.Typography.caption(), secondary: true),
    ])
    heading.spacing = PaxTheme.Spacing.xs
    addArrangedSubview(heading)
    let text = valueField
    metricTitle = title
    metricCaption = caption
    let font =
      failed
      ? PaxTheme.Routes.failureFont
      : style == .usage ? PaxTheme.Routes.usageFont : PaxTheme.Routes.metricFont
    let attributes = NSMutableAttributedString(
      string: value,
      attributes: [
        .font: PaxTheme.Typography.metric(size: font, weight: .medium),
        .foregroundColor: failed
          ? PaxTheme.Color.error
          : valueColor ?? (value == "—" ? PaxTheme.Routes.muted : PaxTheme.Routes.text),
      ])
    if !unit.isEmpty {
      attributes.append(
        NSAttributedString(
          string: " " + unit,
          attributes: [
            .font: PaxTheme.Typography.caption(), .foregroundColor: PaxTheme.Routes.muted,
          ]))
    }
    valueAttributes = attributes.attributes(at: 0, effectiveRange: nil)
    text.attributedStringValue = attributes
    let numberRow = NSStackView(views: [text, NSView()])
    numberRow.spacing = PaxTheme.Routes.metricGap
    numberRow.alignment = .centerY
    if samples.count >= 3 && !failed {
      let mini: NSView
      if bars {
        mini = PaxMiniBars(values: samples, color: color, summary: title + "，单位 " + unit)
      } else {
        let view = PaxTrendView()
        view.presentation = .rangeSparkline
        view.color = color
        view.samples = samples.enumerated().map {
          .init(
            value: $0.element,
            label:
              "第 \($0.offset + 1) 次 · \($0.element.formatted(.number.precision(.fractionLength(0...2)))) "
              + unit)
        }
        mini = view
      }
      mini.widthAnchor.constraint(equalToConstant: PaxTheme.Routes.miniWidth).isActive = true
      mini.heightAnchor.constraint(equalToConstant: PaxTheme.Routes.miniHeight).isActive = true
      let label = PaxRouteText.label(
        "近 \(samples.count) 次",
        font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.captionFont), secondary: true)
      label.alignment = .right
      let plot = NSStackView(views: [mini, label])
      plot.orientation = .vertical
      plot.alignment = .trailing
      plot.spacing = PaxTheme.Spacing.xs
      numberRow.addArrangedSubview(plot)
    }
    addArrangedSubview(numberRow)
    numberRow.widthAnchor.constraint(equalTo: widthAnchor).isActive = true
    if style == .test {
      numberRow.heightAnchor.constraint(greaterThanOrEqualToConstant: PaxTheme.Routes.metricLine)
        .isActive = true
    }
    if !caption.isEmpty {
      let label = PaxRouteText.label(caption, font: PaxTheme.Typography.caption(), secondary: true)
      if caption.hasPrefix("实时") { label.textColor = PaxTheme.Color.healthy }
      addArrangedSubview(label)
    }
    setAccessibilityLabel(
      [title, value, unit, caption].filter { !$0.isEmpty }.joined(separator: "，"))
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
}

/// 详情中的小图标标题，不强制使用 Dashboard 的大图标座。
public final class PaxRouteSectionHeading: NSStackView {
  public init(_ title: String, symbol: String, detail: String = "") {
    super.init(frame: .zero)
    orientation = .horizontal
    spacing = PaxTheme.Spacing.tight
    addArrangedSubview(PaxRouteIcon.view(symbol, size: PaxTheme.Routes.nameFont))
    addArrangedSubview(
      PaxRouteText.label(
        title, font: PaxTheme.Typography.label(size: PaxTheme.Routes.bodyFont, weight: .medium)))
    addArrangedSubview(NSView())
    if !detail.isEmpty {
      addArrangedSubview(
        PaxRouteText.label(detail, font: PaxTheme.Typography.caption(), secondary: true))
    }
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
}

/// 指标列等宽，分隔线与16pt内边距共用在测试结果与使用记录。
public final class PaxRouteMetricRow: NSStackView {
  private var previous: NSView?
  public init(_ metrics: [NSView]) {
    super.init(frame: .zero)
    orientation = .horizontal
    alignment = .top
    distribution = .fill
    spacing = PaxTheme.Spacing.regular
    for metric in metrics { addMetric(metric) }
  }
  public func addMetric(_ metric: NSView) {
    let preceding = previous
    if preceding != nil {
      let divider = NSView()
      divider.wantsLayer = true
      divider.layer?.backgroundColor = PaxTheme.Routes.line.cgColor
      addArrangedSubview(divider)
      divider.widthAnchor.constraint(equalToConstant: PaxTheme.Layout.cardBorder).isActive = true
      divider.heightAnchor.constraint(equalTo: heightAnchor).isActive = true
    }
    addArrangedSubview(metric)
    if let preceding {
      metric.widthAnchor.constraint(equalTo: preceding.widthAnchor).isActive = true
    }
    previous = metric
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
}

/// 图例形状和系列颜色同时对应图表，不用灰色文字圆点替代。
public final class PaxRouteLegend: NSStackView {
  public init(_ entries: [(String, NSColor)]) {
    super.init(frame: .zero)
    orientation = .horizontal
    alignment = .centerY
    spacing = PaxTheme.Spacing.medium
    for (text, color) in entries {
      let mark = NSView()
      mark.wantsLayer = true
      mark.layer?.backgroundColor = color.cgColor
      mark.layer?.cornerRadius = PaxTheme.Chart.proportionGap
      mark.widthAnchor.constraint(equalToConstant: PaxTheme.Routes.legendSize).isActive = true
      mark.heightAnchor.constraint(equalToConstant: PaxTheme.Routes.legendSize).isActive = true
      let label = PaxRouteText.label(text, font: PaxTheme.Typography.caption(), secondary: true)
      let row = NSStackView(views: [mark, label])
      row.spacing = PaxTheme.Spacing.xs
      addArrangedSubview(row)
    }
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
}
