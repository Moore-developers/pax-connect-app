import AppKit

/// 分组表单共用的说明/当前值/控件行，只接收呈现值，不认识设置Store。
public final class PaxPreferenceRow: NSView {
  public enum Style { case standard, compact }

  private let detailField: NSTextField
  private let titleField: NSTextField
  private let feedbackField = PaxText.label(
    "", font: PaxTheme.Typography.caption(), secondary: true)
  private let currentField = PaxText.label("", font: PaxTheme.Typography.caption())
  public init(title: String, detail: String, control: NSView, style: Style = .standard) {
    detailField = PaxText.label(
      detail, font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
    titleField = PaxText.label(
      title, font: PaxTheme.Typography.label(size: PaxTheme.Routes.bodyFont))
    super.init(frame: .zero)
    detailField.isHidden = detail.isEmpty
    currentField.textColor = PaxTheme.Color.accent
    currentField.isHidden = true
    titleField.setContentCompressionResistancePriority(.defaultHigh, for: .horizontal)
    feedbackField.lineBreakMode = .byTruncatingTail
    feedbackField.isHidden = true
    let heading = NSStackView(views: [titleField, feedbackField, NSView()])
    heading.alignment = .centerY
    heading.spacing = PaxTheme.Spacing.tight
    let copy = NSStackView(views: [heading, detailField, currentField])
    copy.orientation = .vertical
    copy.alignment = .leading
    copy.spacing = PaxTheme.Spacing.xs
    for field in [heading, detailField, currentField] {
      field.widthAnchor.constraint(equalTo: copy.widthAnchor).isActive = true
      field.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    }
    control.setContentHuggingPriority(.required, for: .horizontal)
    control.setContentCompressionResistancePriority(.required, for: .horizontal)
    copy.translatesAutoresizingMaskIntoConstraints = false
    control.translatesAutoresizingMaskIntoConstraints = false
    addSubview(copy)
    addSubview(control)
    let inset = style == .compact ? PaxTheme.Settings.compactRowInset : PaxTheme.Spacing.medium
    let minimum =
      style == .compact ? PaxTheme.Settings.compactRowMinimum : PaxTheme.Settings.rowMinimum
    NSLayoutConstraint.activate([
      copy.leadingAnchor.constraint(equalTo: leadingAnchor),
      copy.trailingAnchor.constraint(
        equalTo: control.leadingAnchor, constant: -PaxTheme.Spacing.regular),
      control.trailingAnchor.constraint(equalTo: trailingAnchor),
      control.centerYAnchor.constraint(equalTo: centerYAnchor),
      heightAnchor.constraint(greaterThanOrEqualToConstant: minimum),
    ])
    if style == .compact {
      NSLayoutConstraint.activate([
        copy.topAnchor.constraint(greaterThanOrEqualTo: topAnchor, constant: inset),
        copy.bottomAnchor.constraint(lessThanOrEqualTo: bottomAnchor, constant: -inset),
        copy.centerYAnchor.constraint(equalTo: centerYAnchor),
      ])
    } else {
      NSLayoutConstraint.activate([
        copy.topAnchor.constraint(equalTo: topAnchor, constant: inset),
        copy.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -inset),
      ])
    }
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public func apply(title: String, detail: String) {
    titleField.stringValue = title
    detailField.stringValue = detail
    detailField.isHidden = detail.isEmpty
  }
  public func apply(feedback: String) {
    feedbackField.stringValue = feedback
    feedbackField.toolTip = feedback.isEmpty ? nil : feedback
    feedbackField.isHidden = feedback.isEmpty
  }
  public func apply(detail: String, currentValue: String?) {
    detailField.stringValue = detail
    detailField.isHidden = detail.isEmpty
    currentField.stringValue = currentValue.map { "当前：" + $0 } ?? ""
    currentField.isHidden = currentValue == nil
  }
}
