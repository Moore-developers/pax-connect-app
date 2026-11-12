import AppKit
import CoreImage
import PaxUI

/// 只呈现调用方导出的完整分享链接；线路读取与协议编码由调用方负责。
@MainActor
final class RouteShareController: RouteModalController {
  override var maximumPresentationWidth: CGFloat? { PaxTheme.RouteShare.width }
  private let loadLink: @MainActor () async throws -> String
  private var loadTask: Task<Void, Never>?
  private var closed = false
  private var shareLink: String?
  private let qr = RouteShareCodeView()
  private var qrHeight: NSLayoutConstraint?
  private let linkView = NSTextView()
  private let linkScroll = NSScrollView()
  private let linkCard = RouteShareLinkSurface()
  private let message = NSTextField(wrappingLabelWithString: "正在生成分享链接…")
  private lazy var copyButton = button("复制链接", #selector(copyLink), primary: true)
  private lazy var retryButton = button("重试", #selector(retry))

  init(name: String, loadLink: @escaping @MainActor () async throws -> String) {
    self.loadLink = loadLink
    super.init(
      title: "分享二维码", subtitle: name,
      size: NSSize(width: PaxTheme.RouteShare.width, height: PaxTheme.Routes.picker.height),
      style: .compact, symbol: "qrcode")
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  override func loadView() {
    super.loadView()
    if let titleRow = pane.identity.superview as? NSStackView {
      let icon = PaxRouteIconTile("qrcode", tone: .blue, size: PaxTheme.Routes.pickerIcon)
      titleRow.insertArrangedSubview(icon, at: 0)
      titleRow.spacing = PaxTheme.Spacing.tight
      icon.widthAnchor.constraint(equalToConstant: PaxTheme.Routes.pickerIcon).isActive = true
      icon.heightAnchor.constraint(equalToConstant: PaxTheme.Routes.pickerIcon).isActive = true
    }
    pane.content.spacing = PaxTheme.Spacing.tight
    message.font = PaxTheme.Typography.caption()
    message.alignment = .center
    message.textColor = PaxTheme.Color.textSecondary
    message.setAccessibilityIdentifier("route-share-state")
    qr.setAccessibilityElement(true)
    qr.setAccessibilityRole(.image)
    qr.setAccessibilityLabel("线路分享二维码")
    qr.setAccessibilityIdentifier("route-share-qr")
    pane.add(qr)
    qrHeight = qr.heightAnchor.constraint(equalToConstant: PaxTheme.RouteShare.codeSide)
    qrHeight?.isActive = true
    pane.add(message)
    linkScroll.translatesAutoresizingMaskIntoConstraints = false
    linkCard.addSubview(linkScroll)
    NSLayoutConstraint.activate([
      linkScroll.leadingAnchor.constraint(
        equalTo: linkCard.leadingAnchor, constant: PaxTheme.Spacing.tight),
      linkScroll.trailingAnchor.constraint(
        equalTo: linkCard.trailingAnchor, constant: -PaxTheme.Spacing.tight),
      linkScroll.topAnchor.constraint(
        equalTo: linkCard.topAnchor, constant: PaxTheme.Spacing.tight),
      linkScroll.bottomAnchor.constraint(
        equalTo: linkCard.bottomAnchor, constant: -PaxTheme.Spacing.tight),
    ])
    pane.add(linkCard)
    linkView.isEditable = false
    linkView.isSelectable = true
    linkView.isRichText = false
    linkView.font = PaxTheme.Typography.label(size: PaxTheme.Routes.bodyFont)
    linkView.textContainerInset = .zero
    linkView.textContainer?.lineFragmentPadding = 0
    linkView.textColor = PaxTheme.Color.text
    linkView.drawsBackground = false
    linkView.isVerticallyResizable = true
    linkView.isHorizontallyResizable = false
    linkView.autoresizingMask = [.width]
    linkView.frame = NSRect(
      x: 0, y: 0, width: preferredContentSize.width - PaxTheme.Routes.bodyInset * 2,
      height: PaxTheme.RouteShare.linkHeight)
    linkView.textContainer?.widthTracksTextView = true
    linkView.textContainer?.lineBreakMode = .byCharWrapping
    linkView.setAccessibilityLabel("完整分享链接，可选择并复制")
    linkView.setAccessibilityIdentifier("route-share-link")
    linkScroll.hasVerticalScroller = true
    linkScroll.borderType = .noBorder
    linkScroll.drawsBackground = false
    linkScroll.contentView.drawsBackground = false
    linkScroll.documentView = linkView
    linkScroll.heightAnchor.constraint(equalToConstant: PaxTheme.RouteShare.linkHeight).isActive =
      true
    let privacy = NSTextField(wrappingLabelWithString: "包含敏感连接信息，仅分享给可信任的人。")
    privacy.font = PaxTheme.Typography.caption()
    privacy.textColor = PaxTheme.Color.textSecondary
    privacy.alignment = .center
    pane.add(privacy)
    beginLoading()
  }

  private func beginLoading() {
    loadTask?.cancel()
    shareLink = nil
    copyButton.isEnabled = false
    qr.isHidden = true
    linkCard.isHidden = true
    message.stringValue = "正在生成分享链接…"
    pane.status.stringValue = ""
    pane.setActions([button("关闭", #selector(close)), copyButton])
    let load = loadLink
    loadTask = Task { @MainActor [weak self] in
      do {
        let link = try await load()
        guard !Task.isCancelled, let self, !self.closed else { return }
        guard !link.isEmpty else {
          self.showLoadFailure()
          return
        }
        self.showLink(link)
      } catch {
        guard !Task.isCancelled, let self, !self.closed else { return }
        // 仅呈现明确的本地产品说明，不显示或记录未知错误上下文。
        self.showLoadFailure((error as? RouteWorkbenchStore.EditError)?.message)
      }
    }
  }

  private func showLoadFailure(_ explanation: String? = nil) {
    message.stringValue = explanation ?? "无法获取分享链接，请重试。"
    pane.setActions([button("关闭", #selector(close)), retryButton])
  }

  private func showLink(_ link: String) {
    shareLink = link
    linkView.string = link
    linkCard.isHidden = false
    copyButton.isEnabled = true
    if qr.apply(link) {
      qrHeight?.constant = qr.renderedSide
      qr.isHidden = false
      message.stringValue = "扫码导入，或复制完整链接"
    } else {
      message.stringValue = "链接过长或无法编码为二维码。仍可复制完整链接到导入窗口。"
    }
    pane.invalidateContentSize()
    // 呈现容器按新内容测量后再定位一次，避免初始 clip origin 沿用布局中的偏移。
    Task { @MainActor [weak self] in
      guard let self, !self.closed else { return }
      self.view.layoutSubtreeIfNeeded()
      if let container = self.linkView.textContainer {
        self.linkView.layoutManager?.ensureLayout(for: container)
      }
      self.linkView.setSelectedRange(NSRange(location: 0, length: 0))
      self.linkView.scrollRangeToVisible(NSRange(location: 0, length: 0))
      self.linkScroll.contentView.scroll(to: .zero)
      self.linkScroll.reflectScrolledClipView(self.linkScroll.contentView)
    }
  }

  @objc private func retry() { beginLoading() }
  @objc private func copyLink() {
    guard let shareLink else { return }
    NSPasteboard.general.clearContents()
    let copied = NSPasteboard.general.setString(shareLink, forType: .string)
    pane.status.stringValue = copied ? "已复制链接" : "复制失败，请选择链接后重试"
    pane.status.textColor = copied ? PaxTheme.Color.textSecondary : PaxTheme.Color.error
    pane.status.isHidden = false
    pane.status.superview?.isHidden = false
    pane.setActions([button("关闭", #selector(close)), copyButton])
  }

  override func close() {
    stopLoading()
    super.close()
  }
  override func viewDidDisappear() {
    super.viewDidDisappear()
    stopLoading()
  }
  private func stopLoading() {
    closed = true
    loadTask?.cancel()
    loadTask = nil
  }
}

extension PaxTheme {
  fileprivate enum RouteShare {
    static let width: CGFloat = 368
    static let codeSide: CGFloat = 300
    static let linkHeight: CGFloat = 64
  }
}

/// 按二维码模块的整数倍设备像素绘制，四周至少保留四个白色模块。
private final class RouteShareCodeView: NSView {
  private var code: CGImage?
  var renderedSide: CGFloat {
    guard let code else { return PaxTheme.RouteShare.codeSide }
    let scale = window?.backingScaleFactor ?? 1
    let modulesWithQuietZone = CGFloat(code.width) + 8
    let availableSide = min(
      PaxTheme.RouteShare.codeSide, bounds.width > 0 ? bounds.width : PaxTheme.RouteShare.codeSide)
    let pixels = floor(availableSide * scale / modulesWithQuietZone)
    return modulesWithQuietZone * max(1, pixels) / scale
  }
  func apply(_ link: String) -> Bool {
    code = nil
    needsDisplay = true
    let data = Data(link.utf8)
    // QR version 40 / M 的 byte 模式容量；不可截断或降低纠错掩盖超限。
    guard data.count <= 2_331,
      let filter = CIFilter(name: "CIQRCodeGenerator")
    else { return false }
    filter.setValue(data, forKey: "inputMessage")
    filter.setValue("M", forKey: "inputCorrectionLevel")
    guard let output = filter.outputImage,
      let image = CIContext().createCGImage(output, from: output.extent)
    else { return false }
    code = image
    return true
  }
  override func draw(_ dirtyRect: NSRect) {
    guard let code, let context = NSGraphicsContext.current?.cgContext else { return }
    let scale = window?.backingScaleFactor ?? 1
    let quietModules: CGFloat = 4
    let modules = CGFloat(code.width)
    let pixels = floor(min(bounds.width, bounds.height) * scale / (modules + quietModules * 2))
    guard pixels >= 1 else { return }
    let moduleSide = pixels / scale
    let side = (modules + quietModules * 2) * moduleSide
    let origin = CGPoint(
      x: floor((bounds.midX - side / 2) * scale) / scale,
      y: floor((bounds.midY - side / 2) * scale) / scale)
    context.saveGState()
    context.setShouldAntialias(false)
    context.interpolationQuality = .none
    // 黑白是二维码编码要求，不随界面明暗主题改变。
    context.setFillColor(NSColor.white.cgColor)
    context.fill(CGRect(origin: origin, size: CGSize(width: side, height: side)))
    context.draw(
      code,
      in: CGRect(
        x: origin.x + quietModules * moduleSide, y: origin.y + quietModules * moduleSide,
        width: modules * moduleSide, height: modules * moduleSide))
    context.restoreGState()
  }
}

/// 使用 AppKit 绘制面板，避免滚动视图的内部图层覆盖背景与边框。
private final class RouteShareLinkSurface: NSView {
  override func draw(_ dirtyRect: NSRect) {
    let inset = PaxTheme.Layout.cardBorder / 2
    let path = NSBezierPath(
      roundedRect: bounds.insetBy(dx: inset, dy: inset),
      xRadius: PaxTheme.Radius.control, yRadius: PaxTheme.Radius.control)
    PaxTheme.Routes.canvas.setFill()
    path.fill()
    PaxTheme.Routes.edge.setStroke()
    path.lineWidth = PaxTheme.Layout.cardBorder
    path.stroke()
  }
}

extension RouteWorkbenchViewController {
  func shareRoute(_ id: String) {
    guard let node = store.node(id) else { return }
    let routeStore = store
    presentRouteModal(
      RouteShareController(name: node.name) { try await routeStore.loadShareLink(id) })
  }
}
