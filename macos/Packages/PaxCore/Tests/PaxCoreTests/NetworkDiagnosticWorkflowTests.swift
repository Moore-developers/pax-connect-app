import Foundation
import PaxProto
import Testing

@testable import PaxClient
@testable import PaxCore

@MainActor @Suite("完整诊断与分项证据") struct NetworkDiagnosticWorkflowTests {
  private func route(_ mode: String, ip: String = "8.8.8.8", error: String = "")
    -> NetworkDiagnosticReport
  {
    NetworkDiagnosticReport(
      .with {
        $0.targetRouteID = "route"
        $0.configRevision = 1
        $0.network = "network"
        $0.mode = mode
        $0.at = .init(date: Date())
        $0.providerError = error
        $0.exit = .with { $0.ip = ip }
        if error.isEmpty { $0.score = 55 }
        $0.probes = [
          .with {
            $0.target = "https://chatgpt.com/"
            $0.httpStatus = 200
            $0.state = "responded"
            $0.at = .init(date: Date())
          }
        ]
      })
  }
  private func settle(_ store: NetworkDiagnosticStore) async throws {
    for _ in 0..<100 where store.busy { try await Task.sleep(for: .milliseconds(5)) }
    #expect(!store.busy)
  }

  @Test("顶部一次触发环境与完整模式，局部重测不会清除其他成功记录")
  func fullAndPartial() async throws {
    actor Requests {
      var modes: [String] = []
      var actions: [String] = []
      func mode(_ value: String) { modes.append(value) }
      func action(_ value: String) { actions.append(value) }
    }
    let requests = Requests()
    let environment = EnvironmentDiagnosticStore(
      request: { action, _ in
        await requests.action(action)
        return EnvironmentDiagnosticReport(
          .with {
            $0.sessionID = "session"
            $0.mac = .with {
              $0.complete = true
              $0.ipv4 = "8.8.8.8"
            }
            $0.complete = true
          })
      }, openBrowser: { _ in false })
    let full = route("full")
    let service = route("services")
    let quick = route("quick", error: "target_connection_timeout")
    let store = NetworkDiagnosticStore(
      currentEnvironment: environment,
      run: { mode, _ in
        await requests.mode(mode)
        return mode == "services" ? service : mode == "quick" ? quick : full
      })
    store.startAll()
    try await settle(store)
    #expect(await requests.modes == ["full"])
    #expect(await requests.actions.contains("start"))
    store.start(mode: "services")
    try await settle(store)
    store.start(mode: "quick")
    try await settle(store)
    #expect(store.services == service)
    #expect(store.report?.providerError == "target_connection_timeout")
    #expect(store.intelligenceReport?.score == 55)
    #expect(store.previousReport == full)
    store.clear()
  }

  @Test("资料局部重测只在同出口保留旧请求，换 IP 不借用资料")
  func sourcesIdentity() async throws {
    actor Queue {
      var items: [NetworkDiagnosticReport]
      init(_ values: [NetworkDiagnosticReport]) { items = values }
      func next() -> NetworkDiagnosticReport { items.removeFirst() }
    }
    let original = route("full")
    let same = route("sources", error: "target_connection_timeout").preservingProbes([])
    let changed = route("sources", ip: "1.1.1.1", error: "target_connection_timeout")
      .preservingProbes([])
    let queue = Queue([original, same, changed])
    let store = NetworkDiagnosticStore(run: { _, _ in await queue.next() })
    store.start()
    try await settle(store)
    store.start(mode: "sources")
    try await settle(store)
    #expect(store.report?.probes.count == 1)
    #expect(store.intelligenceReport?.score == 55)
    store.start(mode: "sources")
    try await settle(store)
    #expect(store.report?.probes.isEmpty == true)
    #expect(store.intelligenceReport?.score == nil)
    #expect(store.intelligenceReport?.exit?.ip == "1.1.1.1")
    store.clear()
  }

  @Test("旧 JSON 缺少新增证据仍可恢复，不能猜原因")
  func legacyHistory() throws {
    let folder = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    defer { try? FileManager.default.removeItem(at: folder) }
    let url = folder.appendingPathComponent("latest.json")
    let history = NetworkDiagnosticHistory(url: url)
    history.saveRoute(report: route("full"), services: nil, environment: nil)
    history.saveEnvironment(
      EnvironmentDiagnosticReport(
        .with {
          $0.mac = .with {
            $0.complete = true
            $0.ipv4 = "8.8.8.8"
          }
        }), local: nil)
    var json = try #require(
      JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [String: Any])
    var routeJSON = try #require(json["route"] as? [String: Any])
    var report = try #require(routeJSON["report"] as? [String: Any])
    var probes = try #require(report["probes"] as? [[String: Any]])
    for i in probes.indices {
      for key in ["state", "stage", "redirectHost"] { probes[i].removeValue(forKey: key) }
    }
    report["probes"] = probes
    routeJSON["report"] = report
    routeJSON.removeValue(forKey: "previous")
    json["route"] = routeJSON
    var env = try #require(json["environment"] as? [String: Any])
    var envReport = try #require(env["report"] as? [String: Any])
    for key in ["mac", "browser"] {
      var sample = try #require(envReport[key] as? [String: Any])
      sample.removeValue(forKey: "checks")
      envReport[key] = sample
    }
    env["report"] = envReport
    env.removeValue(forKey: "previous")
    json["environment"] = env
    try JSONSerialization.data(withJSONObject: json).write(to: url)
    let reopened = NetworkDiagnosticHistory(url: url)
    #expect(reopened.message == nil)
    #expect(reopened.route?.report?.probes.first?.state == nil)
    #expect(reopened.environment?.report.mac.checks == nil)
    let family = EnvironmentDiagnosticPresentation.familyValue(
      reopened.environment?.report.mac, v6: true)
    #expect(family.contains("旧记录未采集"))
  }

  @Test("预算未执行不冒充 HTTP0，浏览器证据脱敏并保留终止原因")
  func evidenceAndExport() async throws {
    let report = route("full")
    let emptyProbe = NetworkDiagnosticReport(
      .with {
        $0.probes = [
          .with {
            $0.state = "not_started"
            $0.errorCode = "diagnostic_budget_exhausted"
            $0.stage = "budget"
          }
        ]
      }
    ).probes[0]
    #expect(NetworkDiagnosticPresentation.probeResult(emptyProbe).contains("未发起请求"))
    #expect(!NetworkDiagnosticPresentation.probeResult(emptyProbe).contains("HTTP 0"))
    let raw = EnvironmentDiagnosticReport(
      .with {
        $0.complete = true
        $0.mac = .with {
          $0.complete = true
          $0.ipv4 = "8.8.8.8"
        }
        $0.browser = .with {
          $0.complete = true
          $0.rtcState = "empty"
          $0.checks = [
            .with {
              $0.kind = "echo_cf"
              $0.target = "www.cloudflare.com"
              $0.state = "observed"
              $0.value = "1.1.1.1 · LAX"
            },
            .with {
              $0.kind = "rtc_cf"
              $0.target = "stun.cloudflare.com:3478"
              $0.state = "timeout"
              $0.errorCode = "rtc_server_701"
            },
          ]
        }
      })
    let store = EnvironmentDiagnosticStore(request: { _, _ in raw }, openBrowser: { _ in false })
    store.start()
    for _ in 0..<100 where store.busy { try await Task.sleep(for: .milliseconds(15)) }
    let text = EnvironmentDiagnosticPresentation.markdown(store)
    #expect(text.contains("STUN 错误 701"))
    #expect(!text.contains("1.1.1.1"))
    #expect(!text.contains("8.8.8.8"))
    #expect(report.mode == "full")
    store.clear()
  }
}
