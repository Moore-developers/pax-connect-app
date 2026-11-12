import AppKit

/// 无业务的文字、分组和说明呈现，用于工作台与组件目录。
@MainActor
public enum PaxText {
  /// Table-only typography matches the route list; charts and summary cards keep their own type.
  public static func tableLabel(
    _ text: String, secondary: Bool = false, numeric: Bool = false, identity: Bool = false,
    alignment: NSTextAlignment = .left
  ) -> NSTextField {
    let font =
      secondary
      ? (numeric ? PaxTheme.Typography.time : PaxTheme.Typography.caption())
      : numeric
        ? PaxTheme.Typography.metric(size: PaxTheme.Table.font)
        : PaxTheme.Typography.label(
          size: identity ? PaxTheme.Routes.nameFont : PaxTheme.Table.font,
          weight: identity ? .medium : .regular)
    let label = PaxText.label(text, font: font)
    label.textColor = secondary ? PaxTheme.Table.muted : PaxTheme.Table.text
    label.alignment = alignment
    label.lineBreakMode = .byTruncatingTail
    label.maximumNumberOfLines = 1
    label.toolTip = text
    return label
  }
  public static func label(
    _ text: String, font: NSFont = PaxTheme.Typography.label(), secondary: Bool = false,
    wrapping: Bool = false
  ) -> NSTextField {
    let field =
      wrapping
      ? NSTextField(wrappingLabelWithString: text) : NSTextField(labelWithString: text)
    field.font = font
    field.textColor = secondary ? PaxTheme.Color.textSecondary : PaxTheme.Color.text
    if wrapping { field.maximumNumberOfLines = 0 }
    field.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    return field
  }
}

/// 区块标题的结构与业务无关，动作使用系统按钮。
public final class PaxSectionHeader: NSStackView {
  public init(_ title: String, detail: String? = nil, action: NSView? = nil) {
    super.init(frame: .zero)
    orientation = .horizontal
    alignment = .centerY
    spacing = PaxTheme.Spacing.tight
    addArrangedSubview(PaxText.label(title, font: PaxTheme.Typography.section))
    addArrangedSubview(NSView())
    if let detail {
      addArrangedSubview(
        PaxText.label(detail, font: PaxTheme.Typography.caption(), secondary: true))
    }
    if let action { addArrangedSubview(action) }
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
}

/// 数值呈现不负责计算与格式化；缺数据用明确的文案，不转换成零。
public final class PaxMetricView: NSStackView {
  private let caption = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private let value = PaxText.label("", font: PaxTheme.Typography.metric())
  private let compact: Bool
  private let context = PaxText.label(
    "", font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  public init(
    label: String, value: String, unit: String = "", context: String = "", compact: Bool = false
  ) {
    self.compact = compact
    super.init(frame: .zero)
    orientation = .vertical
    alignment = .leading
    spacing = PaxTheme.Spacing.tight
    self.value.font = compact ? PaxTheme.Typography.secondaryMetric : PaxTheme.Typography.metric()
    addArrangedSubview(caption)
    if compact {
      let row = NSStackView(views: [self.value, NSView(), self.context])
      row.orientation = .horizontal
      row.alignment = .bottom
      row.spacing = PaxTheme.Spacing.regular
      self.context.alignment = .right
      addArrangedSubview(row)
      row.widthAnchor.constraint(equalTo: widthAnchor).isActive = true
    } else {
      addArrangedSubview(self.value)
      addArrangedSubview(self.context)
    }
    apply(label: label, value: value, unit: unit, context: context)
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
  public func apply(label: String, value: String, unit: String = "", context: String = "") {
    caption.stringValue = label
    caption.isHidden = label.isEmpty
    let text = NSMutableAttributedString(
      string: value,
      attributes: [
        .font: compact ? PaxTheme.Typography.secondaryMetric : PaxTheme.Typography.metric(),
        .foregroundColor: PaxTheme.Color.text,
      ])
    if !unit.isEmpty {
      text.append(
        NSAttributedString(
          string: " " + unit,
          attributes: [
            .font: PaxTheme.Typography.label(
              size: compact ? PaxTheme.Control.compactUnit : PaxTheme.Control.unit),
            .foregroundColor: PaxTheme.Color.textSecondary,
          ]))
    }
    self.value.attributedStringValue = text
    self.context.stringValue = context
    self.context.isHidden = context.isEmpty
    setAccessibilityLabel(
      [label, value, unit, context].filter { !$0.isEmpty }.joined(separator: "，"))
  }
}

/// 本地选择器，只发选中序号，不知道业务含义。
public final class PaxSegmentedControl: NSSegmentedControl {
  public var onSelect: ((Int) -> Void)?
  public init(labels: [String], selected: Int = 0) {
    super.init(frame: .zero)
    segmentCount = labels.count
    focusRingType = .none
    trackingMode = .selectOne
    segmentStyle = .rounded
    for (index, label) in labels.enumerated() { setLabel(label, forSegment: index) }
    selectedSegment = selected
    target = self
    action = #selector(changed)
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
  @objc private func changed() { onSelect?(selectedSegment) }
}

/// 提示组件以文字和符号表达状态，不能只靠颜色。
public final class PaxNotice: NSView {
  public enum Tone { case neutral, warning, error }
  private let titleField = PaxText.label(
    "", font: PaxTheme.Typography.label(weight: .medium), wrapping: true)
  private let detailField = PaxText.label("", secondary: true, wrapping: true)
  private let symbol = NSImageView()
  private var tone: Tone = .neutral
  private lazy var fade = PaxFadeVisibility(self)
  /// 带淡变的显示/隐藏；淡出期间不响应点击。
  public func setVisible(_ visible: Bool) { fade.set(visible) }
  public override func hitTest(_ point: NSPoint) -> NSView? {
    fade.isHiding ? nil : super.hitTest(point)
  }
  /// The action sits under the text, never beside it, so narrow cards cannot crush the button.
  public var onAction: (() -> Void)?
  private let actionButton = PaxCompactButton(
    "", style: .bordered, target: nil, action: #selector(performAction))
  public init(title: String, detail: String, tone: Tone = .neutral) {
    super.init(frame: .zero)
    wantsLayer = true
    actionButton.target = self
    actionButton.isHidden = true
    actionButton.setContentCompressionResistancePriority(.required, for: .horizontal)
    let labels = NSStackView(views: [titleField, detailField, actionButton])
    labels.orientation = .vertical
    labels.alignment = .leading
    labels.spacing = PaxTheme.Spacing.xs
    let row = NSStackView(views: [symbol, labels])
    row.alignment = .top
    row.spacing = PaxTheme.Spacing.medium
    row.translatesAutoresizingMaskIntoConstraints = false
    addSubview(row)
    NSLayoutConstraint.activate([
      row.leadingAnchor.constraint(equalTo: leadingAnchor, constant: PaxTheme.Spacing.regular),
      row.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -PaxTheme.Spacing.regular),
      row.topAnchor.constraint(equalTo: topAnchor, constant: PaxTheme.Spacing.regular),
      row.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -PaxTheme.Spacing.regular),
      symbol.widthAnchor.constraint(equalToConstant: PaxTheme.Layout.noticeIcon),
      symbol.heightAnchor.constraint(equalToConstant: PaxTheme.Layout.noticeIcon),
      labels.widthAnchor.constraint(
        greaterThanOrEqualToConstant: PaxTheme.Layout.noticeMinimumText),
    ])
    apply(title: title, detail: detail, tone: tone)
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
  public func setAction(_ title: String?, identifier: String? = nil) {
    actionButton.isHidden = title == nil
    actionButton.title = title ?? ""
    actionButton.setAccessibilityLabel(title)
    if let identifier { actionButton.setAccessibilityIdentifier(identifier) }
  }
  @objc private func performAction() { onAction?() }
  public func apply(title: String, detail: String, tone: Tone) {
    self.tone = tone
    titleField.stringValue = title
    detailField.stringValue = detail
    detailField.isHidden = detail.isEmpty
    symbol.image = NSImage(
      systemSymbolName: tone == .neutral ? "info.circle" : "exclamationmark.triangle",
      accessibilityDescription: nil)
    setAccessibilityLabel(title + "，" + detail)
    refreshSurface()
  }
  private func refreshSurface() {
    let color: NSColor =
      tone == .neutral
      ? PaxTheme.Color.textSecondary
      : tone == .warning ? PaxTheme.Color.warning : PaxTheme.Color.error
    symbol.contentTintColor = color
    effectiveAppearance.performAsCurrentDrawingAppearance {
      layer?.backgroundColor = color.withAlphaComponent(PaxTheme.Color.noticeFillOpacity).cgColor
      layer?.cornerRadius = PaxTheme.Radius.control
    }
  }
  public override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    refreshSurface()
  }
}
