import AppKit
import PaxCore
import PaxUI

@MainActor
final class RouteExitInfoView: NSStackView {
  let context: PresentationContext

  private let address: String
  private let pasteboard: NSPasteboard
  private let originalCopyTitle: String
  private var copyButton: PaxButton?
  private var copyReset: Task<Void, Never>?
  private var copied = false

  init(
    value: ExitLocation, historical: Bool, pasteboard: NSPasteboard = .general,
    context: PresentationContext = .chinese
  ) {
    self.context = context

    self.pasteboard = pasteboard
    originalCopyTitle =
      historical
      ? context.text("network_controls.ff5088d15c") : context.text("network_controls.a29b4bcfa2")
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
      title: historical
        ? context.text("network_controls.44bfe5cbc3") : context.text("network_controls.281bde70cd"),
      tone: historical ? .neutral : .healthy)
    let copy = PaxButton(
      .secondary,
      title: historical
        ? context.text("network_controls.ff5088d15c") : context.text("network_controls.a29b4bcfa2"),
      target: self,
      action: #selector(copyAddress))
    copyButton = copy
    copy.setAccessibilityIdentifier("copy-exit-ip")
    let header = NSStackView(views: [ip, badge, NSView(), copy])
    header.alignment = .centerY
    header.spacing = PaxTheme.Spacing.medium
    ip.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    let region = PaxRouteText.label(
      ExitLocationPresentation.region(value, context: context), secondary: historical,
      wrapping: true)
    region.setAccessibilityIdentifier("exit-region")
    let isp = RouteDetailField(
      context.text("network_controls.b6713f8098"),
      value: value.isp.isEmpty ? context.text("network_controls.756762e293") : value.isp,
      identifier: "exit-isp")
    let asn = RouteDetailField(
      context.text("network_controls.ab1d048a1f"),
      value: value.asn.isEmpty ? context.text("network_controls.756762e293") : value.asn,
      identifier: "exit-asn")
    let source = RouteDetailField(
      context.text("network_controls.3c6acb97c9"),
      value: value.source + " · " + MeasurementTimestamp.secondsText(value.at),
      identifier: "exit-source")
    let geo = RouteDetailField(
      context.text("network_controls.89f6d2b1d1"),
      value: value.geoSource.isEmpty
        ? context.text("network_controls.756762e293")
        : value.geoSource + " · " + MeasurementTimestamp.text(value.geoAt),
      identifier: "exit-geo-source")
    let grid = PaxFormGrid(rows: [[isp, asn], [source, geo]])
    grid.setAccessibilityIdentifier("exit-properties")
    for field in [isp, asn, source, geo] {
      field.value.font = PaxTheme.Typography.caption()
      if historical { field.value.textColor = PaxTheme.Routes.muted }
    }
    let note = PaxRouteText.label(
      (value.geoError.isEmpty ? "" : context.text("network_controls.1aed306e17"))
        + context.text("network_controls.29a77f11ef"),
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
    copyButton?.title =
      alreadyCopied
      ? context.text("network_controls.8e3d529798") : context.text("network_controls.8f6f8d979c")
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
