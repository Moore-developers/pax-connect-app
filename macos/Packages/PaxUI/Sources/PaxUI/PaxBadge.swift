import AppKit

/// 状态胶囊：语义色的淡底 + 同系文字（颜色表达状态，不表达国家）。
public final class PaxBadge: NSTextField {
  public enum Tone: Sendable {
    case neutral
    case healthy
    case warning
    case error
    case accent

    fileprivate var color: NSColor {
      switch self {
      case .neutral: PaxTheme.Color.textSecondary
      case .healthy: PaxTheme.Color.healthy
      case .warning: PaxTheme.Color.warning
      case .error: PaxTheme.Color.error
      case .accent: PaxTheme.Color.accent
      }
    }
  }

  public private(set) var tone: Tone

  public init(title: String, tone: Tone = .neutral) {
    self.tone = tone
    super.init(frame: .zero)
    isEditable = false
    isBordered = false
    drawsBackground = false
    stringValue = title
    font = PaxTheme.Typography.label(size: 12, weight: .medium)
    setAccessibilityLabel(title)
    applies(tone: tone)
  }

  @available(*, unavailable)
  public required init?(coder: NSCoder) {
    fatalError("PaxUI 不使用 XIB/Storyboard（code-standards.md §二）")
  }

  public func apply(title: String, tone: Tone) {
    self.tone = tone
    stringValue = title
    applies(tone: tone)
  }

  private func applies(tone: Tone) {
    textColor = tone.color
  }

  public override var intrinsicContentSize: NSSize {
    NSSize(width: super.intrinsicContentSize.width + 4, height: super.intrinsicContentSize.height)
  }
}
