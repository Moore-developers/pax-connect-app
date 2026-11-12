import AppKit
import PaxCore
import PaxUI

@MainActor final class NetworkDiagnosticProgressSymbol: NSView {
  private let spinner = PaxActivityIndicator(frame: .zero)
  private let image = NSImageView()
  override init(frame: NSRect) {
    super.init(frame: frame)
    for view in [spinner, image] { addSubview(view) }
    let size = PaxTheme.NetworkDiagnostics.progressIcon
    widthAnchor.constraint(equalToConstant: size).isActive = true
    heightAnchor.constraint(equalToConstant: size).isActive = true
    setAccessibilityElement(false)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func layout() {
    super.layout()
    spinner.frame = bounds
    image.frame = bounds
  }
  func apply(_ state: NetworkDiagnosticActivity.State) {
    let running = state == .running
    spinner.isHidden = !running
    spinner.isAnimating = running
    image.isHidden = running
    let symbol: String
    let color: NSColor
    switch state {
    case .running:
      symbol = ""
      color = PaxTheme.Color.accent
    case .finished:
      symbol = "checkmark.circle"
      color = PaxTheme.Color.healthy
    case .partial:
      symbol = "exclamationmark.circle"
      color = PaxTheme.Color.amberLabel
    case .failed:
      symbol = "xmark.circle"
      color = PaxTheme.Color.error
    case .stopped:
      symbol = "stop.circle"
      color = PaxTheme.Color.textSecondary
    case .idle:
      symbol = "clock"
      color = PaxTheme.Color.textSecondary
    }
    if !running {
      image.image = NSImage(systemSymbolName: symbol, accessibilityDescription: nil)
      image.contentTintColor = color
    }
    spinner.tintColor = color
  }
  func stop() { spinner.isAnimating = false }
}

@MainActor final class NetworkDiagnosticProgressRow: NSView {
  private typealias Theme = PaxTheme.NetworkDiagnostics
  private let symbol = NetworkDiagnosticProgressSymbol()
  private let name = PaxRouteText.label(
    "", font: PaxTheme.Typography.label(size: Theme.progressBodyFont, weight: .medium))
  private let detail = PaxRouteText.label(
    "", font: PaxTheme.Typography.label(size: Theme.progressBodyFont), secondary: true,
    wrapping: true)
  private let duration = PaxRouteText.label("", font: PaxTheme.Typography.time, secondary: true)
  override init(frame: NSRect) {
    super.init(frame: frame)
    let label = NSStackView(views: [symbol, name])
    label.spacing = PaxTheme.Spacing.tight
    label.widthAnchor.constraint(equalToConstant: Theme.progressLabelWidth).isActive = true
    name.setContentHuggingPriority(.defaultLow, for: .horizontal)
    detail.maximumNumberOfLines = 0
    detail.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    detail.setContentHuggingPriority(.defaultLow, for: .horizontal)
    duration.alignment = .right
    duration.widthAnchor.constraint(equalToConstant: Theme.progressTimeWidth).isActive = true
    for view in [label, detail, duration] {
      view.translatesAutoresizingMaskIntoConstraints = false
      addSubview(view)
    }
    NSLayoutConstraint.activate([
      heightAnchor.constraint(greaterThanOrEqualToConstant: Theme.progressRowHeight),
      label.leadingAnchor.constraint(equalTo: leadingAnchor),
      label.centerYAnchor.constraint(equalTo: centerYAnchor),
      label.topAnchor.constraint(greaterThanOrEqualTo: topAnchor, constant: Theme.progressRowInset),
      label.bottomAnchor.constraint(
        lessThanOrEqualTo: bottomAnchor, constant: -Theme.progressRowInset),
      detail.leadingAnchor.constraint(
        equalTo: label.trailingAnchor, constant: PaxTheme.Spacing.medium),
      detail.trailingAnchor.constraint(
        equalTo: duration.leadingAnchor, constant: -PaxTheme.Spacing.medium),
      detail.topAnchor.constraint(equalTo: topAnchor, constant: Theme.progressRowInset),
      detail.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -Theme.progressRowInset),
      duration.trailingAnchor.constraint(equalTo: trailingAnchor),
      duration.centerYAnchor.constraint(equalTo: centerYAnchor),
    ])
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  func apply(_ activity: NetworkDiagnosticActivity, elapsed: Int) {
    name.stringValue = activity.name
    detail.stringValue =
      activity.state == .stopped
      ? "已停止"
      : activity.detail.replacingOccurrences(of: "已结束探测", with: "探测已结束")
    detail.textColor =
      activity.state == .failed
      ? PaxTheme.Color.error
      : activity.state == .partial ? PaxTheme.Color.amberLabel : PaxTheme.Color.textSecondary
    symbol.apply(activity.state)
    duration.stringValue = "\(elapsed) 秒"
    setAccessibilityLabel("\(activity.name)，\(detail.stringValue)，\(elapsed) 秒")
  }
  func stop() { symbol.stop() }
}
