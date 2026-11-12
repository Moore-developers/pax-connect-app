import XCTest

@testable import PaxCore

final class ProductErrorPresentationTests: XCTestCase {
  func testCoreStartFailureUsesScrubbedReason() {
    XCTAssertEqual(
      ProductErrorPresentation.message(
        ProductError(code: "core_profile_start_failed", messageParams: ["address_in_use"])),
      "代理内核启动资源被占用，请检查本地代理端口后重试。")
  }

  func testRouteConflictSuggestsManualTUNRelease() {
    let message = ProductErrorPresentation.message(
      ProductError(code: "core_profile_start_failed", messageParams: ["route_conflict"]))
    XCTAssertTrue(message.contains("系统路由冲突"))
    XCTAssertFalse(message.contains("线路配置"))
  }

  func testCoreStartFailureFallsBackForUnknownReason() {
    XCTAssertEqual(
      ProductErrorPresentation.message(
        ProductError(code: "core_profile_start_failed", messageParams: ["unknown"])),
      "代理内核启动失败，尚未确认原因，请查看诊断记录后重试。")
  }
  func testUnknownRuntimeDoesNotClaimTrafficStopped() {
    let message = ProductErrorPresentation.message(
      ProductError(code: "runtime_observation_unavailable"))
    XCTAssertFalse(message.contains("已暂停"))
    XCTAssertTrue(message.contains("实际使用的线路"))
  }

  func testCloudflare52xWSHandshakeIsServerSide() {
    let cases = [
      ("521", "源站拒绝连接"), ("522", "源站连接超时"), ("523", "源站不可达"),
      ("524", "源站响应超时"), ("525", "源站 TLS 握手失败"), ("526", "源站证书无效"),
      ("530", "源站不可用"), ("527", "源站故障"),
    ]
    for (status, reason) in cases {
      let message = ProductErrorPresentation.message(
        ProductError(code: "node_ws_handshake_failed", messageParams: [status]))
      XCTAssertTrue(message.hasPrefix("服务端源站故障（HTTP \(status) · \(reason)）"), message)
      XCTAssertFalse(message.contains("核对 Host"))
    }
  }

  func testOtherWSHandshakeStatusKeepsConfigHint() {
    let message = ProductErrorPresentation.message(
      ProductError(code: "node_ws_handshake_failed", messageParams: ["403"]))
    XCTAssertEqual(message, "WS 握手失败（HTTP 403），请核对 Host、路径与服务端")
  }
}
