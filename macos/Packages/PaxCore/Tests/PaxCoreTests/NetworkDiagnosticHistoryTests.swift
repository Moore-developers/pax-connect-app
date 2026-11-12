import Foundation
import PaxProto
import Testing

@testable import PaxClient
@testable import PaxCore

@MainActor @Suite("线路诊断上次结果") struct NetworkDiagnosticHistoryTests {
  private func directory() -> URL {
    FileManager.default.temporaryDirectory.appendingPathComponent(
      "pax-diagnostic-" + UUID().uuidString)
  }

  private func route(_ name: String) -> NetworkDiagnosticReport {
    NetworkDiagnosticReport(
      .with {
        $0.targetRouteID = name
        $0.targetRouteName = name
        $0.at = .init(date: Date(timeIntervalSince1970: 1_733_011_200))
        $0.score = 55
        $0.exit = .with { $0.ip = "203.0.113.15" }
        $0.sources = [
          .with {
            $0.name = "example-source"
            $0.country = "US"
          }
        ]
      })
  }

  private func environment(_ ip: String, session: String = "secret-session")
    -> EnvironmentDiagnosticReport
  {
    EnvironmentDiagnosticReport(
      .with {
        $0.sessionID = session
        $0.browserURL = "http://127.0.0.1:1234/secret-token/"
        $0.at = .init(date: Date(timeIntervalSince1970: 1_733_011_200))
        $0.complete = true
        $0.mac = .with {
          $0.complete = true
          $0.ipv4 = ip
        }
        $0.browser = .with {
          $0.complete = true
          $0.browser = "Chrome"
          $0.ipv4 = ip
        }
      })
  }

  @Test("本地恢复环境与线路记录，且不保存会话凭据")
  func restoreAndPrivacy() throws {
    let folder = directory()
    defer { try? FileManager.default.removeItem(at: folder) }
    let url = folder.appendingPathComponent("latest.json")
    let history = NetworkDiagnosticHistory(url: url)
    #expect(history.message == nil)
    let local = NetworkDiagnosticEnvironment(
      timezone: "America/Los_Angeles", languages: ["en-US"], region: "US", at: Date())
    history.saveEnvironment(environment("203.0.113.4"), local: local)
    history.saveRoute(report: route("route-one"), services: route("services"), environment: local)
    #expect(history.message == nil)
    let data = try Data(contentsOf: url)
    let json = try #require(String(data: data, encoding: .utf8))
    #expect(!json.contains("secret-session"))
    #expect(!json.contains("secret-token"))
    #expect(!json.contains("browserURL"))
    #expect(
      (try FileManager.default.attributesOfItem(atPath: url.path)[.posixPermissions] as? Int)
        == 0o600)
    #expect(
      (try FileManager.default.attributesOfItem(atPath: folder.path)[.posixPermissions] as? Int)
        == 0o700)

    let reopened = NetworkDiagnosticHistory(url: url)
    let environmentStore = EnvironmentDiagnosticStore(
      history: reopened, request: { _, _ in throw ClientError.unavailable },
      openBrowser: { _ in false })
    let networkStore = NetworkDiagnosticStore(
      currentEnvironment: environmentStore, history: reopened,
      run: { _, _ in throw ClientError.unavailable })
    #expect(environmentStore.report?.mac.ipv4 == "203.0.113.4")
    #expect(environmentStore.report?.browser.browser == "Chrome")
    #expect(environmentStore.report?.sessionID == "")
    #expect(environmentStore.local == local)
    #expect(environmentStore.stale)
    #expect(networkStore.report?.targetRouteName == "route-one")
    #expect(networkStore.services?.targetRouteName == "services")
    #expect(networkStore.stale)
  }

  @Test("新测试取消后保留上次结果，迟到响应不会覆盖")
  func cancelledRunRetainsResult() async throws {
    let folder = directory()
    defer { try? FileManager.default.removeItem(at: folder) }
    let url = folder.appendingPathComponent("latest.json")
    let history = NetworkDiagnosticHistory(url: url)
    history.saveEnvironment(environment("203.0.113.4"), local: nil)
    history.saveRoute(report: route("old"), services: nil, environment: nil)
    let newer = environment("203.0.113.8", session: "second")
    let environmentStore = EnvironmentDiagnosticStore(
      history: history, request: { _, _ in newer }, openBrowser: { _ in true })
    let newerRoute = route("new")
    let networkStore = NetworkDiagnosticStore(
      currentEnvironment: environmentStore, history: history,
      run: { _, _ in
        try? await Task.sleep(for: .milliseconds(80))
        return newerRoute
      })
    environmentStore.start()
    networkStore.start()
    for _ in 0..<100 where environmentStore.report?.mac.ipv4 != "203.0.113.8" { await Task.yield() }
    #expect(environmentStore.report?.mac.ipv4 == "203.0.113.8")
    networkStore.retainLastResult()
    try await Task.sleep(for: .milliseconds(120))
    #expect(environmentStore.report?.mac.ipv4 == "203.0.113.4")
    #expect(networkStore.report?.targetRouteName == "old")
    #expect(!networkStore.busy && !environmentStore.busy)
    #expect(environmentStore.stale && networkStore.stale)
    let reopened = NetworkDiagnosticHistory(url: url)
    #expect(reopened.environment?.report.mac.ipv4 == "203.0.113.4")
    #expect(reopened.route?.report?.targetRouteName == "old")
  }

  @Test("成功检测覆盖上次线路记录，失败不会覆盖")
  func successAndFailure() async throws {
    let folder = directory()
    defer { try? FileManager.default.removeItem(at: folder) }
    let url = folder.appendingPathComponent("latest.json")
    let history = NetworkDiagnosticHistory(url: url)
    history.saveRoute(report: route("old"), services: nil, environment: nil)
    let newer = route("new")
    let store = NetworkDiagnosticStore(history: history, run: { _, _ in newer })
    store.start()
    for _ in 0..<200 where store.busy { await Task.yield() }
    #expect(!store.busy)
    #expect(NetworkDiagnosticHistory(url: url).route?.report?.targetRouteName == "new")
    let failing = NetworkDiagnosticStore(
      history: history, run: { _, _ in throw ClientError.unavailable })
    failing.start()
    for _ in 0..<200 where failing.busy { await Task.yield() }
    #expect(failing.report?.targetRouteName == "new")
    #expect(NetworkDiagnosticHistory(url: url).route?.report?.targetRouteName == "new")
  }

  @Test("环境检测结束后保存，可由新实例恢复")
  func environmentCompletion() async throws {
    let folder = directory()
    defer { try? FileManager.default.removeItem(at: folder) }
    let url = folder.appendingPathComponent("latest.json")
    let history = NetworkDiagnosticHistory(url: url)
    let result = environment("203.0.113.9")
    let store = EnvironmentDiagnosticStore(
      history: history, request: { _, _ in result }, openBrowser: { _ in true })
    store.start()
    for _ in 0..<250 where store.busy { try await Task.sleep(for: .milliseconds(10)) }
    #expect(!store.busy)
    let reopened = NetworkDiagnosticHistory(url: url)
    #expect(reopened.environment?.report.mac.ipv4 == "203.0.113.9")
    #expect(reopened.environment?.report.sessionID == "")
  }

  @Test("损坏记录不被覆盖，保存失败可见")
  func failures() throws {
    let folder = directory()
    defer { try? FileManager.default.removeItem(at: folder) }
    try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
    let corrupt = folder.appendingPathComponent("corrupt.json")
    let content = Data("invalid snapshot".utf8)
    try content.write(to: corrupt)
    let history = NetworkDiagnosticHistory(url: corrupt)
    #expect(history.message?.contains("无法读取") == true)
    history.saveRoute(report: route("new"), services: nil, environment: nil)
    #expect(try Data(contentsOf: corrupt) == content)
    let impossible = NetworkDiagnosticHistory(url: corrupt.appendingPathComponent("latest.json"))
    impossible.saveRoute(report: route("new"), services: nil, environment: nil)
    #expect(impossible.message != nil)
  }
}
