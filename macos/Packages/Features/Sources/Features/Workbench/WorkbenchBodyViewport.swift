import AppKit

/// Keeps page widths independent of the window when a display is narrower than the workbench.
@MainActor
final class WorkbenchBodyViewport: NSView {
  private let page: NSView
  private let minimumWidth: CGFloat
  private let overflow = NSScrollView()
  private let document = WorkbenchDocument()

  init(content: NSView, minimumWidth: CGFloat) {
    page = content
    self.minimumWidth = minimumWidth
    super.init(frame: .zero)
    overflow.drawsBackground = false
    overflow.automaticallyAdjustsContentInsets = false
    overflow.hasHorizontalScroller = true
    overflow.scrollerStyle = .legacy
    overflow.documentView = document
    // The viewport sets the page frame before its internal constraints are solved.
    page.translatesAutoresizingMaskIntoConstraints = true
    addSubview(page)
    page.frame = bounds
  }

  @available(*, unavailable)
  required init?(coder: NSCoder) { fatalError("代码构建") }

  override func resizeSubviews(withOldSize oldSize: NSSize) {
    super.resizeSubviews(withOldSize: oldSize)
    guard bounds.width > 0, bounds.height > 0 else { return }
    if bounds.width < minimumWidth {
      overflow.frame = bounds
      document.setFrameSize(NSSize(width: minimumWidth, height: overflow.contentSize.height))
      if overflow.superview == nil {
        addSubview(overflow)
        document.addSubview(page)
        overflow.contentView.scroll(to: .zero)
        overflow.reflectScrolledClipView(overflow.contentView)
      }
      page.frame = document.bounds
    } else {
      if overflow.superview != nil {
        addSubview(page)
        overflow.removeFromSuperview()
      }
      page.frame = bounds
    }
  }
}
