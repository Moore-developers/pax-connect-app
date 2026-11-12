import Foundation
import PaxClient

extension ConnectionStore {
  public var canCheckCurrentPath: Bool {
    connection == .connected && proxyEnabled && status != .starting && currentPathCommand != nil
      && currentPathSupported && !currentPathRequesting && currentPathCheck.state != "checking"
      && currentPathCheck.cause != "device_sleeping"
  }

  public func recheckCurrentPath() async {
    guard canCheckCurrentPath, let currentPathCommand else { return }
    currentPathRequesting = true
    defer { currentPathRequesting = false }
    do {
      _ = try await currentPathCommand(UUID().uuidString)
      currentPathMessage = ""
    } catch { currentPathMessage = ProductErrorPresentation.display(error) }
  }

  public var currentPathSummary: String {
    guard connection == .connected else { return "后台未连接 · 显示最后状态" }
    guard proxyEnabled else { return "代理已关闭" }
    if currentPathCheck.cause == "device_sleeping" { return "系统休眠 · 通道尚未验证" }
    switch currentPathCheck.state {
    case "waiting_network": return "代理已开启 · 等待网络恢复"
    case "checking": return "代理已开启 · 正在验证"
    case "passed": return "当前通道可用"
    case "failed": return "当前通道检查失败"
    case "stale": return "连接已变化，请重新验证"
    case "inconclusive":
      return currentPathCheck.cause == "physical_network_wait"
        ? "代理已开启 · 等待网络恢复" : "暂未确认连通"
    default: return "代理已开启 · 尚未确认连通"
    }
  }

  public var currentPathVerificationTime: String? {
    guard connection == .connected, proxyEnabled, currentPathCheck.state == "passed",
      let at = currentPathCheck.completedAt
    else { return nil }
    return "验证于 " + TrafficFormatting.time(at)
  }
}
