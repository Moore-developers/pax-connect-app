import AppKit
import QuartzCore

/// 滑块位移的一次运行：起点是被打断时的当前显示位置。
struct PaxThumbRun: Equatable {
  var from: CGFloat
  var to: CGFloat
  var start: CFTimeInterval
  var step: PaxMotion.Step

  func position(at now: CFTimeInterval) -> CGFloat {
    guard step.duration > 0 else { return to }
    let progress = (now - start) / step.duration
    guard progress < 1 else { return to }
    let eased = step.curve?.value(at: progress) ?? progress
    return from + (to - from) * CGFloat(eased)
  }
  func finished(at now: CFTimeInterval) -> Bool { now - start >= step.duration }
}

extension PaxTabs {
  /// 状态（selectedIndex / AX）已是终值；这里只推进绘制用的 `thumbPosition`。
  /// 从当前显示位置出发，打断即接管；减少动态效果、无窗口或无可见位移时即时到位。
  func animateThumb(to target: CGFloat, now: CFTimeInterval = CACurrentMediaTime()) {
    stopThumbDriver()
    let step = PaxMotion.plan(.indicator).primary
    guard window != nil, step.duration > 0, thumbPosition != target else {
      thumbPosition = target
      needsDisplay = true
      return
    }
    thumbRun = PaxThumbRun(from: thumbPosition, to: target, start: now, step: step)
    let link = displayLink(target: self, selector: #selector(thumbTick(_:)))
    link.add(to: .main, forMode: .common)
    thumbLink = link
  }

  /// 按给定时刻推进一帧；到时落终值并停止驱动。
  func advanceThumb(now: CFTimeInterval) {
    guard let run = thumbRun else { return }
    thumbPosition = run.position(at: now)
    needsDisplay = true
    if run.finished(at: now) {
      thumbRun = nil
      stopThumbDriver()
    }
  }

  /// 停止动画并保持当前显示位置（拖动接管时用）。
  func cancelThumbAnimation() {
    thumbRun = nil
    stopThumbDriver()
  }

  /// 停止动画并落到终值（离开窗口时用）。
  func settleThumb() {
    thumbRun = nil
    stopThumbDriver()
    thumbPosition = CGFloat(selectedIndex)
    needsDisplay = true
  }

  private func stopThumbDriver() {
    thumbLink?.invalidate()
    thumbLink = nil
  }

  @objc private func thumbTick(_ link: CADisplayLink) {
    advanceThumb(now: link.targetTimestamp)
  }
}
