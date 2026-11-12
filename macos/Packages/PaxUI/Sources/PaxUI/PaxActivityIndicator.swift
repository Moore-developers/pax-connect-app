import AppKit
import QuartzCore

/// A small AppKit activity arc. It never intercepts its parent control's clicks.
public final class PaxActivityIndicator: NSView {
  public var tintColor = PaxTheme.Color.accent { didSet { needsDisplay = true } }
  public var isAnimating = false {
    didSet {
      guard oldValue != isAnimating else { return }
      updateAnimation()
    }
  }
  private let arc = CAShapeLayer()

  public override init(frame: NSRect) {
    super.init(frame: frame)
    wantsLayer = true
    layer?.addSublayer(arc)
    setAccessibilityElement(false)
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var wantsUpdateLayer: Bool { true }
  public override func hitTest(_ point: NSPoint) -> NSView? { nil }
  public override func layout() {
    super.layout()
    CATransaction.begin()
    CATransaction.setDisableActions(true)
    arc.frame = bounds
    let inset = PaxTheme.ActivityIndicator.lineWidth / 2
    let radius = max(0, min(bounds.width, bounds.height) / 2 - inset)
    let path = CGMutablePath()
    path.addArc(
      center: CGPoint(x: bounds.midX, y: bounds.midY), radius: radius,
      startAngle: 0, endAngle: .pi * 1.5, clockwise: false)
    arc.path = path
    arc.fillColor = nil
    arc.lineWidth = PaxTheme.ActivityIndicator.lineWidth
    arc.lineCap = .round
    CATransaction.commit()
  }
  public override func updateLayer() {
    arc.strokeColor = tintColor.cgColor
  }
  public override func viewDidMoveToWindow() {
    super.viewDidMoveToWindow()
    updateAnimation()
  }
  public override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }
  private func updateAnimation() {
    arc.removeAnimation(forKey: "activity")
    guard isAnimating, window != nil, !PaxMotion.reducesMotion else { return }
    let rotation = CABasicAnimation(keyPath: "transform.rotation.z")
    rotation.fromValue = 0
    rotation.toValue = Double.pi * 2
    rotation.duration = PaxTheme.ActivityIndicator.period
    rotation.repeatCount = .infinity
    arc.add(rotation, forKey: "activity")
  }
}

extension PaxTheme {
  public enum ActivityIndicator {
    public static let lineWidth: CGFloat = 1.25
    public static let period: TimeInterval = 0.8
  }
}
