import AppKit
import PaxCore
import PaxUI

/// Liquid Glass 内容面板：整块内容悬浮在透明窗口上，边缘留出桌面（macOS 26 样式）。
///
/// 玻璃包住内容（S0-④ 约束），页内不再叠玻璃；窗口底色透明，由桌面透出。
@MainActor
public final class GlassContentController: NSViewController {
  public init(content: NSViewController) {
    super.init(nibName: nil, bundle: nil)
    let glass = GlassSurface(style: .regular, cornerRadius: PaxTheme.Radius.card)
    // 轻微白色提亮：深色桌面上也保持明亮的磨砂观感。
    glass.tintColor = NSColor.white.withAlphaComponent(0.05)
    let host = content.view
    host.translatesAutoresizingMaskIntoConstraints = false
    glass.contentView = host
    glass.translatesAutoresizingMaskIntoConstraints = false
    view = NSView()
    view.addSubview(glass)
    NSLayoutConstraint.activate([
      glass.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: 8),
      glass.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -8),
      glass.topAnchor.constraint(equalTo: view.topAnchor, constant: 8),
      glass.bottomAnchor.constraint(equalTo: view.bottomAnchor, constant: -8),
    ])
  }

  @available(*, unavailable)
  public required init?(coder: NSCoder) {
    fatalError("界面全部代码构建（code-standards.md §二）")
  }
}
