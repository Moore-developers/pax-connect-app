import AppKit
import PaxCore
import PaxUI

@MainActor
final class RouteExitInfoView: NSStackView {
  private let address: String
  private let pasteboard: NSPasteboard
  private let originalCopyTitle: String
  private var copyButton: PaxButton?
  private var copyReset: Task<Void, Never>?
  private var copied = false

  init(value: ExitLocation, historical: Bool, pasteboard: NSPasteboard = .general) {
    self.pasteboard = pasteboard
    originalCopyTitle = historical ? "复制历史IP" : "复制IP"
    address = value.ip
    super.init(frame: .zero)
    orientation = .vertical
    alignment = .leading
    spacing = PaxTheme.Spacing.medium
    setAccessibilityIdentifier(historical ? "exit-history" : "exit-current")
    let ip = PaxRouteText.label(
      value.ip, font: PaxTheme.Typography.metric(size: 24), secondary: historical, wrapping: true)
    ip.isSelectable = true
    ip.setAccessibilityIdentifier("exit-address")
    let badge = PaxBadge(
      title: historical ? "上次成功 · 历史" : "本次查询", tone: historical ? .neutral : .healthy)
    let copy = PaxButton(
      .secondary, title: historical ? "复制历史IP" : "复制IP", target: self,
      action: #selector(copyAddress))
    copyButton = copy
    copy.setAccessibilityIdentifier("copy-exit-ip")
    let header = NSStackView(views: [ip, badge, NSView(), copy])
    header.alignment = .centerY
    header.spacing = PaxTheme.Spacing.medium
    ip.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    let region = PaxRouteText.label(
      ExitLocationPresentation.region(value), secondary: historical, wrapping: true)
    region.setAccessibilityIdentifier("exit-region")
    let isp = RouteDetailField(
      "网络运营商", value: value.isp.isEmpty ? "未提供" : value.isp, identifier: "exit-isp")
    let asn = RouteDetailField(
      "自治系统（ASN）", value: value.asn.isEmpty ? "未提供" : value.asn, identifier: "exit-asn")
    let source = RouteDetailField(
      "IP 查询", value: value.source + " · " + MeasurementTimestamp.secondsText(value.at),
      identifier: "exit-source")
    let geo = RouteDetailField(
      "地区来源",
      value: value.geoSource.isEmpty
        ? "未提供" : value.geoSource + " · " + MeasurementTimestamp.text(value.geoAt),
      identifier: "exit-geo-source")
    let grid = PaxFormGrid(rows: [[isp, asn], [source, geo]])
    grid.setAccessibilityIdentifier("exit-properties")
    for field in [isp, asn, source, geo] {
      field.value.font = PaxTheme.Typography.caption()
      if historical { field.value.textColor = PaxTheme.Routes.muted }
    }
    let note = PaxRouteText.label(
      (value.geoError.isEmpty ? "" : "地区信息不完整。") + "地区为 IP 数据库估计，可能与实际机房位置不同。",
      font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
    for child in [header, region, grid, note] {
      addArrangedSubview(child)
      child.widthAnchor.constraint(equalTo: widthAnchor).isActive = true
    }
  }

  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  @objc private func copyAddress() {
    let alreadyCopied = pasteboard.string(forType: .string) == address
    if !alreadyCopied {
      pasteboard.clearContents()
      guard pasteboard.setString(address, forType: .string) else { return }
    }
    copyButton?.title = alreadyCopied ? "已经在剪贴板了" : "已复制"
    if alreadyCopied && copied { return }
    copied = true
    copyReset?.cancel()
    let resetAt = ContinuousClock.now.advanced(by: .seconds(3))
    copyReset = Task { @MainActor [weak self] in
      do { try await Task.sleep(until: resetAt, clock: .continuous) } catch { return }
      guard let self else { return }
      self.copied = false
      self.copyButton?.title = self.originalCopyTitle
      self.copyReset = nil
    }
  }
}
