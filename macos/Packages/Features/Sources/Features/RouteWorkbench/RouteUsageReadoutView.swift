import AppKit
import PaxCore
import PaxUI

/// Reads while the popover is visible. Leaving cancels the refresh and any in-flight request.
@MainActor final class RouteUsageReadoutView: NSView, PaxResizableReadout {
  var onReadoutResize: (() -> Void)?
  private let progress: PaxRouteAlertReadout
  private let monitor: RouteUsageMonitor
  private let id: String
  private let context: PresentationContext
  private let content: PaxUsageReadout
  private var task: Task<Void, Never>?
  private var page: RouteUsagePage?
  private var dimension = 0
  private var status: String
  init(monitor: RouteUsageMonitor, id: String, name: String, context: PresentationContext) {
    self.monitor = monitor
    self.id = id
    self.context = context
    status = context.text("routes.usage_loading")
    progress = PaxRouteAlertReadout(
      comparison: context.text("route_progress.compare"),
      direction: context.text("route_progress.direction"),
      detailHint: context.text("route_progress.detail_hint"),
      used: context.text("route_progress.used"), upload: context.text("routes.usage_upload"),
      download: context.text("routes.usage_download"))
    content = PaxUsageReadout(
      title: name, updated: "",
      scope: context.text("route_progress.scope"), progress: progress,
      ranking: context.text("routes.usage_top"),
      dimensions: ["routes.usage_applications", "routes.usage_domains", "routes.usage_ips"].map {
        context.text($0)
      },
      columns: [
        "routes.usage_object", "routes.usage_upload", "routes.usage_download", "routes.usage_total",
      ].map { context.text($0) })
    super.init(frame: .zero)
    content.translatesAutoresizingMaskIntoConstraints = false
    addSubview(content)
    NSLayoutConstraint.activate([
      content.leadingAnchor.constraint(equalTo: leadingAnchor),
      content.trailingAnchor.constraint(equalTo: trailingAnchor),
      content.topAnchor.constraint(equalTo: topAnchor),
      content.bottomAnchor.constraint(equalTo: bottomAnchor),
    ])
    content.onSelect = { [weak self] index in
      self?.dimension = index
      self?.render()
    }
    progress.onResize = { [weak self] in self?.onReadoutResize?() }
    progress.apply(focus: nil, periods: [], note: status)
    render()
  }
  isolated deinit { task?.cancel() }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func viewDidMoveToWindow() {
    super.viewDidMoveToWindow()
    task?.cancel()
    task = nil
    guard window != nil else { return }
    task = Task { [weak self] in
      while !Task.isCancelled {
        await self?.refresh()
        do { try await Task.sleep(for: .seconds(5)) } catch { return }
        guard self?.window?.isVisible == true else { return }
      }
    }
  }
  private func refresh() async {
    do {
      let page = try await monitor.breakdown(id)
      guard !Task.isCancelled else { return }
      self.page = page
      let from = page.from.map(TrafficFormatting.time) ?? "—"
      let until = page.sampledAt.map(TrafficFormatting.time) ?? "—"
      status =
        context.text("routes.usage_range", ["from": from, "until": until])
        + (page.partial ? " · " + context.text("routes.usage_partial") : "")
    } catch {
      guard !Task.isCancelled else { return }
      page = nil
      status = context.text(
        "routes.usage_failed", ["error": ProductErrorPresentation.display(error)])
    }
    RouteAlertProgressPresentation.apply(
      page?.alertProgress, to: progress, context: context,
      unavailable: context.text("route_progress.unavailable"))
    render()
  }
  private func render() {
    let rows =
      page.map { dimension == 0 ? $0.applications : dimension == 1 ? $0.domains : $0.ips } ?? []
    let total = page.map { $0.upload + $0.download } ?? 0
    let ranks: [PaxUsageReadout.Rank] = rows.enumerated().map { index, row in
      let share =
        total == 0
        ? "0%"
        : (Double(row.upload + row.download) / Double(total)).formatted(
          .percent.precision(.fractionLength(1)))
      return .init(
        id: row.id, name: "\(index + 1)  " + row.name,
        upload: TrafficFormatting.bytes(row.upload),
        download: TrafficFormatting.bytes(row.download),
        total: TrafficFormatting.bytes(row.upload + row.download) + "\n" + share)
    }
    let updated =
      page?.sampledAt.map {
        context.text("routes.usage_updated", ["time": TrafficFormatting.time($0)])
      } ?? ""
    content.apply(
      updated: updated,
      status: status, rows: ranks,
      emptyText: page == nil ? status : context.text("routes.usage_empty"))
    onReadoutResize?()
  }
}
