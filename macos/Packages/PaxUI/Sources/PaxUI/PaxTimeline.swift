import AppKit

/// 事件时间线只负责呈现：稳定 ID、时间、标题、说明与语义级别由调用者传入。
public final class PaxTimeline: NSStackView {
  public struct Entry {
    public let id: String
    public let time: String
    public let title: String
    public let detail: String
    public let explanation: String
    public let tone: PaxBadge.Tone
    public init(
      id: String, time: String, title: String, detail: String, tone: PaxBadge.Tone = .neutral,
      explanation: String? = nil
    ) {
      self.id = id
      self.time = time
      self.title = title
      self.detail = detail
      self.explanation = explanation ?? detail
      self.tone = tone
    }
  }
  public var onSelect: ((String) -> Void)?
  private var entries: [Entry] = []
  public init(entries: [Entry]) {
    super.init(frame: .zero)
    orientation = .vertical
    alignment = .leading
    spacing = PaxTheme.Spacing.regular
    apply(entries: entries)
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
  public func apply(entries: [Entry]) {
    for item in arrangedSubviews {
      removeArrangedSubview(item)
      item.removeFromSuperview()
    }
    self.entries = entries
    if entries.isEmpty {
      addArrangedSubview(PaxText.label("还没有运行事件", secondary: true))
      return
    }
    for (index, entry) in entries.enumerated() {
      let time = PaxText.label(
        entry.time, font: PaxTheme.Typography.time, secondary: true)
      let badge = PaxBadge(title: "●", tone: entry.tone)
      let title = PaxButton(
        .ghost, title: entry.title, target: self, action: #selector(openEntry(_:)))
      title.tag = index
      title.contentTintColor = PaxTheme.Color.text
      title.alignment = .left
      title.setAccessibilityLabel(entry.time + "，" + entry.title + "，" + entry.detail + "，查看详情")
      let labels = NSStackView(views: [
        title,
        PaxText.label(
          entry.detail, font: PaxTheme.Typography.caption(), secondary: true, wrapping: true),
      ])
      labels.orientation = .vertical
      labels.alignment = .leading
      labels.spacing = PaxTheme.Spacing.xs
      let row = NSStackView(views: [time, badge, labels])
      row.orientation = .horizontal
      row.alignment = .top
      row.spacing = PaxTheme.Spacing.medium
      addArrangedSubview(row)
      time.widthAnchor.constraint(equalToConstant: PaxTheme.Layout.timeColumn).isActive = true
      row.widthAnchor.constraint(equalTo: widthAnchor).isActive = true
    }
  }
  @objc private func openEntry(_ sender: NSButton) {
    guard entries.indices.contains(sender.tag) else { return }
    onSelect?(entries[sender.tag].id)
  }
}
