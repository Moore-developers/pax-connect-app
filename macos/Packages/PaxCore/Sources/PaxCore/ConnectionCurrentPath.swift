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

  public var currentPathSummary: String { currentPathSummary(context: .chinese) }
  public func currentPathSummary(context: PresentationContext) -> String {
    guard connection == .connected else { return context.text("network_controls.3922a28064") }
    guard proxyEnabled else { return context.text("network_controls.29a31f33c0") }
    if currentPathCheck.cause == "device_sleeping" {
      return context.text("network_controls.f116cc9e73")
    }
    switch currentPathCheck.state {
    case "waiting_network": return context.text("network_controls.da9fdc1a1e")
    case "checking": return context.text("network_controls.8ddd9b79e3")
    case "passed": return context.text("network_controls.d3db8fb9a0")
    case "failed": return context.text("network_controls.52b005b46c")
    case "stale": return context.text("network_controls.37d64f39a9")
    case "inconclusive":
      return currentPathCheck.cause == "physical_network_wait"
        ? context.text("network_controls.da9fdc1a1e") : context.text("network_controls.05fd8d8653")
    default: return context.text("network_controls.b63f6d873e")
    }
  }

  public var currentPathVerificationTime: String? { currentPathVerificationTime(context: .chinese) }
  public func currentPathVerificationTime(context: PresentationContext) -> String? {
    guard connection == .connected, proxyEnabled, currentPathCheck.state == "passed",
      let at = currentPathCheck.completedAt
    else { return nil }
    return context.text("network_controls.67dabf54cd") + TrafficFormatting.time(at)
  }
}
