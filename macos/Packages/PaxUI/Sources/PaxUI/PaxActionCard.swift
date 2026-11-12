import AppKit

/// 行动卡的值输入：只在需要用户操作时出现，由调用方决定内容与按钮。
public struct PaxActionModel: Equatable {
  public enum Tone: Equatable, Sendable { case info, error, warning }
  public var id: String
  public var symbol: String
  public var tone: Tone
  public var title: String
  public var detail: String
  public var primary: String
  public var secondary: String?
  public init(
    id: String, symbol: String, tone: Tone, title: String, detail: String, primary: String,
    secondary: String? = nil
  ) {
    self.id = id
    self.symbol = symbol
    self.tone = tone
    self.title = title
    self.detail = detail
    self.primary = primary
    self.secondary = secondary
  }
}

/// 页头下方的行动卡：图标、标题 / 说明与至多两个按钮（次按钮在左）。
public final class PaxActionCard: PaxCard {
  private var iconView = PaxIcon.view("info.circle", size: PaxTheme.Dashboard.nodeTitle)
  private let title = PaxText.label(
    "", font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyTitle - 1, weight: .medium))
  private let detail = PaxText.label(
    "", font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyFont), secondary: true)
  private let primary = PaxButton(.prominent, title: "")
  private let secondary = PaxButton(.secondary, title: "")
  private var tone = PaxActionModel.Tone.info
  public var onPrimary: (() -> Void)?
  public var onSecondary: (() -> Void)?
  public override var isFlipped: Bool { true }

  public init() {
    super.init(insets: NSEdgeInsets())
    for v in [iconView, title, detail, secondary, primary] { addSubview(v) }
    title.lineBreakMode = .byTruncatingTail
    detail.lineBreakMode = .byTruncatingTail
    primary.target = self
    primary.action = #selector(tapPrimary)
    secondary.target = self
    secondary.action = #selector(tapSecondary)
    setAccessibilityIdentifier("dashboard-action-card")
    primary.setAccessibilityIdentifier("dashboard-action-primary")
    secondary.setAccessibilityIdentifier("dashboard-action-secondary")
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }

  public func apply(_ model: PaxActionModel) {
    iconView.image = PaxIcon.image(model.symbol, size: PaxTheme.Dashboard.nodeTitle)
    tone = model.tone
    title.stringValue = model.title
    detail.stringValue = model.detail
    title.toolTip = model.title
    detail.toolTip = model.detail
    primary.title = model.primary
    secondary.title = model.secondary ?? ""
    secondary.isHidden = model.secondary == nil
    setAccessibilityLabel(model.title + "，" + model.detail)
    refreshTint()
    needsLayout = true
  }

  private func refreshTint() {
    let c = PaxTheme.DashboardColor.self
    iconView.contentTintColor =
      tone == .error ? c.redLabel : tone == .warning ? c.warningText : c.successText
    let base = tone == .error ? c.red : tone == .warning ? c.amber : c.green
    effectiveAppearance.performAsCurrentDrawingAppearance {
      let fill = PaxTheme.Color.card.blended(withFraction: 0.10, of: base) ?? PaxTheme.Color.card
      layer?.backgroundColor = fill.cgColor
    }
  }
  public override func updateLayer() {
    super.updateLayer()
    refreshTint()
  }

  @objc private func tapPrimary() { onPrimary?() }
  @objc private func tapSecondary() { onSecondary?() }

  public override func layout() {
    super.layout()
    let d = PaxTheme.Dashboard.self
    let height = PaxTheme.Control.height
    var right = bounds.width - d.inset
    let primaryWidth = max(primary.intrinsicContentSize.width, PaxTheme.Dashboard.actionButton)
    primary.frame = NSRect(
      x: right - primaryWidth, y: (bounds.height - height) / 2, width: primaryWidth, height: height)
    right = primary.frame.minX - PaxTheme.Spacing.tight
    if !secondary.isHidden {
      let width = max(secondary.intrinsicContentSize.width, PaxTheme.Dashboard.actionButton)
      secondary.frame = NSRect(
        x: right - width, y: (bounds.height - height) / 2, width: width, height: height)
      right = secondary.frame.minX - PaxTheme.Spacing.medium
    }
    iconView.frame = NSRect(
      x: d.inset, y: (bounds.height - d.icon) / 2, width: d.icon, height: d.icon)
    let x = iconView.frame.maxX + PaxTheme.Spacing.medium
    let block = PaxTheme.RTT.textLine * 2
    title.frame = NSRect(
      x: x, y: (bounds.height - block) / 2, width: max(1, right - x), height: PaxTheme.RTT.textLine)
    detail.frame = NSRect(
      x: x, y: title.frame.maxY, width: max(1, right - x), height: PaxTheme.RTT.textLine)
  }
}
