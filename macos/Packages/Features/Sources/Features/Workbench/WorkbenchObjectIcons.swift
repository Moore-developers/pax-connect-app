import AppKit
import Observation
import PaxCore
import PaxUI

/// 本机/内置/后台网站图标的共享有界缓存；不根据 IP 猜应用归属。
@MainActor
@Observable public final class WorkbenchObjectIcons {
  enum Kind: String, Hashable { case application, domain, ip }
  private struct Key: Hashable {
    let kind: Kind
    let id: String
    let name: String
  }
  private struct WaitingFetch {
    let id: UUID
    let continuation: CheckedContinuation<Void, Error>
  }
  @ObservationIgnored private var fetching: Set<UUID> = []
  @ObservationIgnored private var waitingFetches: [WaitingFetch] = []
  private var cache: [Key: NSImage] = [:]
  private var bundledImages: [String: NSImage] = [:]
  fileprivate var iconRevision: UInt64 = 0
  private var presentations: [WeakIconView] = []
  private var onlineImages: [String: (image: NSImage, expires: Date)] = [:]
  private var seenSession: UInt64 = 0
  private var seenInvalidation: UInt64 = 0
  let telemetry: TelemetryStore?
  private let catalog: ObjectIconCatalog
  private let applicationIcon: @MainActor (String, String) -> NSImage?
  public convenience init(telemetry: TelemetryStore? = nil) {
    self.init(applicationIcon: Self.localApplicationIcon, telemetry: telemetry)
  }
  init(
    applicationIcon: @escaping @MainActor (String, String) -> NSImage?,
    catalog: ObjectIconCatalog = .bundled(), telemetry: TelemetryStore? = nil
  ) {
    self.applicationIcon = applicationIcon
    self.catalog = catalog
    self.telemetry = telemetry
  }
  func image(kind: Kind, id: String, name: String) -> NSImage {
    if seenInvalidation != telemetry?.invalidationGeneration ?? 0
      || seenSession != telemetry?.sessionGeneration ?? 0
    {
      onlineImages.removeAll()
      seenInvalidation = telemetry?.invalidationGeneration ?? 0
      seenSession = telemetry?.sessionGeneration ?? 0
    }
    if kind == .domain, let value = onlineImages[name.lowercased()], value.expires > Date() {
      return value.image
    }
    let key = Key(kind: kind, id: id, name: name)
    if let image = cache[key] { return image }
    let native = kind == .application ? applicationIcon(id, name) : nil
    let asset = catalog.asset(kind: kind, id: id, name: name)
    let image = native ?? asset.flatMap { bundledImage($0) }
    let symbol =
      kind == .application
      ? catalog.symbol(id: id, name: name) ?? "app.dashed" : kind == .domain ? "globe" : "network"
    let result = image ?? PaxIcon.image(symbol, size: 32) ?? NSImage()
    if cache.count >= 256 { cache.removeAll(keepingCapacity: true) }
    cache[key] = result
    return result
  }
  func canFetch(kind: Kind, id: String, name: String) -> Bool {
    kind == .domain && catalog.asset(kind: kind, id: id, name: name) == nil
      && telemetry?.iconsAvailable == true
  }
  func load(host: String, fetch: Bool) async throws -> (NSImage?, String) {
    guard let telemetry else { throw ClientError.unavailable }
    let slot = try await acquireFetchSlot()
    defer { releaseFetchSlot(slot) }
    try Task.checkCancellation()
    let value = try await telemetry.objectIcon(host: host, fetch: fetch)
    if value.state == "ready", value.png.count <= 32768, let image = NSImage(data: value.png) {
      if onlineImages.count >= 256 { onlineImages.removeAll() }
      onlineImages[host.lowercased()] = (
        image, value.expiresAt ?? Date().addingTimeInterval(30 * 86400)
      )
      iconRevision &+= 1
      return (image, "网站图标已缓存")
    }
    return (nil, value.state == "unavailable" ? "该站点暂无可用图标，24小时后可重试" : "正在后台补齐网站图标")
  }
  // Presentation admission is separate from daemon network slots: don't drop
  // visible objects merely because a previous pair is still downloading.
  private func acquireFetchSlot() async throws -> UUID {
    try Task.checkCancellation()
    let id = UUID()
    if fetching.count < 2 {
      fetching.insert(id)
      return id
    }
    guard waitingFetches.count < 256 else { throw ProductError(code: "icon_busy") }
    try await withTaskCancellationHandler {
      try await withCheckedThrowingContinuation {
        (continuation: CheckedContinuation<Void, Error>) in
        waitingFetches.append(WaitingFetch(id: id, continuation: continuation))
      }
    } onCancel: {
      Task { @MainActor [weak self] in self?.cancelWaitingFetch(id) }
    }
    return id
  }
  private func cancelWaitingFetch(_ id: UUID) {
    guard let index = waitingFetches.firstIndex(where: { $0.id == id }) else { return }
    waitingFetches.remove(at: index).continuation.resume(throwing: CancellationError())
  }
  private func releaseFetchSlot(_ id: UUID) {
    guard fetching.remove(id) != nil else { return }
    if !waitingFetches.isEmpty {
      let next = waitingFetches.removeFirst()
      fetching.insert(next.id)
      next.continuation.resume()
    }
  }
  func identityView(
    kind: Kind, id: String, name: String, content: NSView,
    size: CGFloat, gap: CGFloat = PaxTheme.Spacing.tight, tint: NSColor? = nil
  ) -> NSView {
    let row = PaxObjectIdentityView(
      image: image(kind: kind, id: id, name: name), content: content,
      iconSize: size, gap: gap, tint: tint)
    guard canFetch(kind: kind, id: id, name: name) else { return row }
    let view = OnlineIdentityView(row: row, icons: self, host: name)
    presentations.removeAll { $0.value == nil }
    if presentations.count >= 256 { presentations.removeFirst() }
    presentations.append(WeakIconView(view))
    return view
  }
  func menu(host: String) -> NSMenu? {
    presentations.first { $0.value?.host == host && $0.value?.window != nil }?.value?.menu
  }
  private func bundledImage(_ name: String) -> NSImage? {
    if let image = bundledImages[name] { return image }
    guard let file = catalog.assets[name]?.file, file == "brands/" + name + ".png",
      let url = ObjectIconCatalog.resourceURL(name),
      let image = NSImage(contentsOf: url)
    else { return nil }
    image.isTemplate = false
    bundledImages[name] = image
    return image
  }
  static func recordedApplicationURL(_ raw: String) -> URL? {
    let path = raw.hasPrefix("process:") ? String(raw.dropFirst(8)) : raw
    guard path.hasPrefix("/") else { return nil }
    let components = URL(fileURLWithPath: path).pathComponents
    guard let index = components.firstIndex(where: { $0.hasSuffix(".app") }) else { return nil }
    return URL(fileURLWithPath: NSString.path(withComponents: Array(components[...index])))
  }
  private static func localApplicationIcon(_ identity: String, _ name: String) -> NSImage? {
    let identity = identity.hasPrefix("process:") ? String(identity.dropFirst(8)) : identity
    var url = Self.recordedApplicationURL(identity)
    if url == nil, !identity.isEmpty, !identity.hasPrefix("/") {
      url = NSWorkspace.shared.urlForApplication(withBundleIdentifier: identity)
    }
    if url == nil, !name.isEmpty, !name.contains("/"), name != ".", name != ".." {
      let roots = [
        "/Applications", NSHomeDirectory() + "/Applications", "/System/Applications",
        "/System/Applications/Utilities",
      ]
      url = roots.map { URL(fileURLWithPath: $0).appendingPathComponent(name + ".app") }
        .first { FileManager.default.fileExists(atPath: $0.path) }
    }
    guard let url, FileManager.default.fileExists(atPath: url.path) else { return nil }
    let source = NSWorkspace.shared.icon(forFile: url.path)
    let image = NSImage(size: NSSize(width: 32, height: 32))
    image.lockFocus()
    source.draw(in: NSRect(x: 0, y: 0, width: 32, height: 32))
    image.unlockFocus()
    image.isTemplate = false
    return image
  }
}

/// One visible object owns one presentation task; cancellation never blocks table rendering.
@MainActor private final class OnlineIdentityView: NSView {
  private let row: PaxObjectIdentityView
  private let icons: WorkbenchObjectIcons
  let host: String
  private var task: Task<Void, Never>?
  private let page: TelemetryPage?
  private var wasVisible = false
  private var lastSession: UInt64 = 0
  private var lastGeneration: UInt64 = 0
  init(row: PaxObjectIdentityView, icons: WorkbenchObjectIcons, host: String) {
    self.row = row
    self.icons = icons
    self.host = host
    self.page = icons.telemetry?.visiblePage
    super.init(frame: .zero)
    addSubview(row)
    let menu = NSMenu()
    let item = NSMenuItem(title: "获取网站图标", action: #selector(fetchIcon), keyEquivalent: "")
    item.target = self
    menu.addItem(item)
    self.menu = menu
    toolTip = "缺图时后台自动补齐；仅匿名访问该站点"
    observePrivacy()
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override var intrinsicContentSize: NSSize { row.intrinsicContentSize }
  override func layout() {
    super.layout()
    row.frame = bounds
  }
  override func viewDidMoveToWindow() {
    super.viewDidMoveToWindow()
    task?.cancel()
    wasVisible =
      window != nil && icons.telemetry?.windowVisible == true && icons.telemetry?.appActive == true
      && icons.telemetry?.visiblePage == page
    if wasVisible { request(fetch: true) }
  }
  private func observePrivacy() {
    withObservationTracking {
      _ = icons.iconRevision
      row.setImage(icons.image(kind: .domain, id: host, name: host))
      let next = icons.telemetry?.invalidationGeneration ?? 0
      if next != lastGeneration {
        lastGeneration = next
        task?.cancel()
        row.setImage(icons.image(kind: .domain, id: host, name: host))
      }
      let session = icons.telemetry?.sessionGeneration ?? 0
      let sessionChanged = session != lastSession
      if sessionChanged {
        lastSession = session
        task?.cancel()
      }
      let windowVisible = icons.telemetry?.windowVisible == true
      let appActive = icons.telemetry?.appActive == true
      let visiblePage = icons.telemetry?.visiblePage
      let visible = window != nil && windowVisible && appActive && visiblePage == page
      if !visible { task?.cancel() }
      let returning = visible && (!wasVisible || sessionChanged)
      wasVisible = visible
      if returning { request(fetch: true) }
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in
        self?.observePrivacy()
      }
    }
  }
  @objc private func fetchIcon() { request(fetch: true) }
  private func request(fetch: Bool) {
    task?.cancel()
    guard icons.telemetry?.windowVisible == true, icons.telemetry?.appActive == true,
      icons.telemetry?.visiblePage == page
    else { return }
    task = Task { [weak self] in
      guard let self else { return }
      do {
        let (image, message) = try await icons.load(host: host, fetch: fetch)
        guard !Task.isCancelled, window != nil else { return }
        if let image { row.setImage(image) }
        toolTip = message
      } catch {
        if !Task.isCancelled { toolTip = "图标暂不可用，请稍后重试" }
      }
    }
  }
}

@MainActor private final class WeakIconView {
  weak var value: OnlineIdentityView?
  init(_ value: OnlineIdentityView) { self.value = value }
}
