import AppKit

/// 分组表单共用的说明/当前值/控件行，只接收呈现值，不认识设置Store。
public final class PaxPreferenceRow: NSView {
  public enum Style { case standard, compact }

  private let detailField: NSTextField
  private let currentField = PaxText.label("", font: PaxTheme.Typography.caption())
  public init(title: String, detail: String, control: NSView, style: Style = .standard) {
    detailField = PaxText.label(
      detail, font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
    super.init(frame: .zero)
    let label = PaxText.label(
      title, font: PaxTheme.Typography.label(size: PaxTheme.Routes.bodyFont))
    currentField.textColor = PaxTheme.Color.accent
    currentField.isHidden = true
    let copy = NSStackView(views: [label, detailField, currentField])
    copy.orientation = .vertical
    copy.alignment = .leading
    copy.spacing = PaxTheme.Spacing.xs
    for field in [label, detailField, currentField] {
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
  public func apply(detail: String, currentValue: String?) {
    detailField.stringValue = detail
    currentField.stringValue = currentValue.map { "当前：" + $0 } ?? ""
    currentField.isHidden = currentValue == nil
  }
}
