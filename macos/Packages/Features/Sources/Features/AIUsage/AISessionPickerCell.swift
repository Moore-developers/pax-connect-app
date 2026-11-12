import AppKit
import PaxCore
import PaxUI

/// Fixed geometry keeps list reuse inexpensive; the directory owns dates and identity.
@MainActor final class AISessionPickerCell: NSView {
  private let icon = NSImageView()
  private let check = NSImageView()
  private let name = PaxText.tableLabel("")
  private let detail = PaxText.tableLabel("", secondary: true)
  private var hovered = false
  private var selected = false

  override var isFlipped: Bool { true }

  override init(frame: NSRect) {
    super.init(frame: frame)
    name.font = PaxTheme.Typography.label(size: PaxTheme.Routes.nameFont)
    for label in [name, detail] {
      label.lineBreakMode = .byTruncatingTail
      addSubview(label)
    }
    icon.imageScaling = .scaleProportionallyUpOrDown
    check.image = PaxIcon.image("checkmark", size: PaxTheme.Routes.bodyFont)
    check.contentTintColor = PaxTheme.Routes.blue
    for image in [icon, check] {
      image.setAccessibilityElement(false)
      addSubview(image)
    }
    addTrackingArea(
      NSTrackingArea(
        rect: .zero, options: [.mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect],
        owner: self))
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  func apply(_ entry: AISessionCatalogEntry, showClient: Bool, selected: Bool) {
    self.selected = selected
    check.isHidden = !selected
    name.stringValue = AISessionPickerController.title(entry, showIdentity: false)
    icon.image = AIClient(code: entry.key.cli).flatMap { AIClientMark.image($0) }
    let time =
      entry.lastActivity.timeIntervalSince1970 > 0
      ? AIUsageFormatting.recentUse(entry.lastActivity, now: entry.lastActivity) : "日期未记录"
    let date =
      entry.lastActivity.timeIntervalSince1970 > 0
      ? AIUsageFormatting.date(entry.lastActivity, time: true) : "日期未记录"
    let issue =
      switch entry.titleStatus {
      case "source_unavailable": " · 名称暂不可读"
      case "source_missing": " · 源记录已移除"
      case "unsupported": " · 格式暂不支持"
      default: ""
      }
    detail.stringValue =
      (showClient ? AIClientMark.shortName(entry.key.cli) + " · " : "") + "最近活动 " + time
      + issue + (entry.unlinkedChild ? " · 未关联子会话" : "")
    toolTip =
      name.stringValue + "\n会话 ID：" + entry.key.session
      + "\n最近活动：" + date
      + (AISessionPickerController.isMessagePreview(entry)
        ? "\n显示内容：最近消息预览（不是会话标题）" : "\n名称来源：" + entry.titleSource)
      + (entry.unlinkedChild ? "\n原生记录确认这是子会话，但无法关联到主会话。保留自身请求供查询。" : "")
    setAccessibilityLabel(name.stringValue)
    setAccessibilityValue(detail.stringValue + (selected ? "，已选中" : ""))
    needsLayout = true
    needsDisplay = true
  }

  override func layout() {
    super.layout()
    let inset = PaxTheme.Spacing.regular
    let iconSize = PaxTheme.Dashboard.iconGlyph
    icon.frame = .init(
      x: inset, y: (bounds.height - iconSize) / 2, width: iconSize, height: iconSize)
    let checkSize = PaxTheme.Routes.bodyFont
    check.frame = .init(
      x: bounds.width - inset - checkSize, y: (bounds.height - checkSize) / 2,
      width: checkSize, height: checkSize)
    let x = icon.frame.maxX + PaxTheme.Spacing.medium
    let width = max(0, check.frame.minX - PaxTheme.Spacing.medium - x)
    let nameHeight = name.intrinsicContentSize.height
    let detailHeight = detail.intrinsicContentSize.height
    let gap = PaxTheme.AISession.contextLineSpacing
    let y = (bounds.height - nameHeight - detailHeight - gap) / 2
    name.frame = .init(x: x, y: y, width: width, height: nameHeight)
    detail.frame = .init(x: x, y: y + nameHeight + gap, width: width, height: detailHeight)
  }

  override func mouseEntered(with event: NSEvent) {
    hovered = true
    needsDisplay = true
  }
  override func mouseExited(with event: NSEvent) {
    hovered = false
    needsDisplay = true
  }
  override func draw(_ dirtyRect: NSRect) {
    guard hovered, !selected, bounds.intersects(visibleRect) else { return }
    PaxTheme.Color.navigationHover.setFill()
    NSBezierPath(
      roundedRect: bounds.insetBy(dx: PaxTheme.Spacing.xs, dy: PaxTheme.Spacing.xs),
      xRadius: PaxTheme.Radius.control, yRadius: PaxTheme.Radius.control
    ).fill()
  }
}
