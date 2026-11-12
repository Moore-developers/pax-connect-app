import AppKit

/// 页面共享标题与右侧主操作；沿用概览的字号、行高及模式控件。
public final class PaxPageHeader: NSView {
  private let titleField: NSTextField
  private let accessory: NSView
  public init(title: String, accessory: NSView) {
    titleField = PaxText.label(title, font: PaxTheme.Typography.title(PaxTheme.Dashboard.pageTitle))
    self.accessory = accessory
    super.init(frame: .zero)
    addSubview(titleField)
    addSubview(accessory)
    titleField.lineBreakMode = .byTruncatingTail
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var isFlipped: Bool { true }
  public override var intrinsicContentSize: NSSize {
    NSSize(width: NSView.noIntrinsicMetric, height: PaxTheme.Dashboard.pageHeader)
  }
  public override func layout() {
    super.layout()
    let size = accessory.intrinsicContentSize
    titleField.frame = NSRect(
      x: 0, y: 0,
      width: max(0, bounds.width - size.width - PaxTheme.Spacing.medium), height: bounds.height)
    accessory.frame = NSRect(
      x: max(0, bounds.width - size.width),
      y: (bounds.height - size.height) / 2, width: size.width, height: size.height)
  }
}
