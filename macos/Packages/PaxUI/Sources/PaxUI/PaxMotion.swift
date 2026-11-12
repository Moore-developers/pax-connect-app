import AppKit
import QuartzCore

/// cubic-bezier 曲线（CSS 同义）。`value(at:)` 供逐帧自绘使用，`timing` 供 Core Animation 使用。
public struct PaxCubicBezier: Equatable, Sendable {
  public let x1: Double, y1: Double, x2: Double, y2: Double
  public init(_ x1: Double, _ y1: Double, _ x2: Double, _ y2: Double) {
    self.x1 = x1
    self.y1 = y1
    self.x2 = x2
    self.y2 = y2
  }
  public var timing: CAMediaTimingFunction {
    CAMediaTimingFunction(controlPoints: Float(x1), Float(y1), Float(x2), Float(y2))
  }
  /// 输入时间进度 0...1，输出位置进度；端点精确为 0 / 1。
  public func value(at time: Double) -> Double {
    if time <= 0 { return 0 }
    if time >= 1 { return 1 }
    var s = time
    for _ in 0..<8 {
      let error = x(s) - time
      if abs(error) < 1e-6 { return y(s) }
      let slope = dx(s)
      if abs(slope) < 1e-6 { break }
      s -= error / slope
    }
    var low = 0.0
    var high = 1.0
    s = time
    for _ in 0..<32 {
      let error = x(s) - time
      if abs(error) < 1e-6 { break }
      if error > 0 { high = s } else { low = s }
      s = (low + high) / 2
    }
    return y(s)
  }
  private func x(_ s: Double) -> Double { bezier(s, x1, x2) }
  private func y(_ s: Double) -> Double { bezier(s, y1, y2) }
  private func bezier(_ s: Double, _ a: Double, _ b: Double) -> Double {
    let u = 1 - s
    return 3 * u * u * s * a + 3 * u * s * s * b + s * s * s
  }
  private func dx(_ s: Double) -> Double {
    let u = 1 - s
    return 3 * u * u * x1 + 6 * u * s * (x2 - x1) + 3 * s * s * (1 - x2)
  }
}

/// 动效入口（motion.md）：减少动态效果的读取源，以及四类场景到时长/曲线的映射。
/// 状态先于动画：调用方的模型值、`isHidden`、AX 在动画开始时即为终值（消失类在收尾时落定）。
@MainActor
public enum PaxMotion {
  public enum Scene: CaseIterable, Sendable {
    case indicator, appear, disappear, expand, presentationOpen, presentationClose
  }
  public enum Kind: Equatable, Sendable { case instant, move, fade, height, fadeScale }
  public struct Step: Equatable, Sendable {
    public var kind: Kind
    public var duration: TimeInterval
    public var curve: PaxCubicBezier?
    public static let instant = Step(kind: .instant, duration: 0, curve: nil)
  }
  public struct Plan: Equatable, Sendable {
    public var primary: Step
    /// 展开后内容的淡入（在主动画到位后开始）。
    public var content: Step?
  }

  /// 减少动态效果的读取源；测试可替换，默认读系统偏好（每次运行时读取）。
  public static var reducesMotionProvider: @MainActor () -> Bool = {
    NSWorkspace.shared.accessibilityDisplayShouldReduceMotion
  }
  public static var reducesMotion: Bool { reducesMotionProvider() }
  /// 测试注入的时长倍数（0 = 全部即时）；生产代码不得修改，用后复位为 1。
  public static var durationScale: Double = 1

  /// 唯一执行入口：按 step 运行 `changes`（对 `animator()` 或可动画属性赋值）。
  /// 时长为 0 时同步执行且同步回调；否则 completion 在动画结束或被新动画接管后各调用一次。
  public static func run(
    _ step: Step, _ changes: () -> Void, completion: (@MainActor () -> Void)? = nil
  ) {
    guard step.duration > 0 else {
      NSAnimationContext.runAnimationGroup { context in
        context.duration = 0
        context.allowsImplicitAnimation = false
        changes()
      }
      completion?()
      return
    }
    NSAnimationContext.runAnimationGroup(
      { context in
        context.duration = step.duration
        context.timingFunction = step.curve?.timing
        context.allowsImplicitAnimation = true
        changes()
      },
      completionHandler: completion.map { done in { MainActor.assumeIsolated { done() } } })
  }

  public static func plan(_ scene: Scene) -> Plan {
    let m = PaxTheme.Motion.self
    let reduced = reducesMotion
    func step(_ kind: Kind, _ duration: TimeInterval, _ curve: PaxCubicBezier) -> Step {
      Step(kind: kind, duration: duration * durationScale, curve: curve)
    }
    switch scene {
    case .indicator:
      return Plan(primary: reduced ? .instant : step(.move, m.standard, m.enter), content: nil)
    case .appear:
      return Plan(primary: step(.fade, m.fast, m.enter), content: nil)
    case .disappear:
      return Plan(primary: step(.fade, m.fast, m.exit), content: nil)
    case .expand:
      return Plan(
        primary: reduced ? .instant : step(.height, m.standard, m.enter),
        content: step(.fade, m.fast, m.enter))
    case .presentationOpen:
      return Plan(
        primary: reduced ? step(.fade, m.fast, m.enter) : step(.fadeScale, m.standard, m.enter),
        content: nil)
    case .presentationClose:
      return Plan(primary: step(.fade, m.fast, m.exit), content: nil)
    }
  }
}
