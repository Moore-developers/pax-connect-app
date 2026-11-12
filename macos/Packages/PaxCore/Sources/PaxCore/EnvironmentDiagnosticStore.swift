import Foundation
import Observation

/// Owns one explicit environment run; route reports retain their own lifetime.
@MainActor @Observable public final class EnvironmentDiagnosticStore {
  public private(set) var previousReport: EnvironmentDiagnosticReport?
  public private(set) var report: EnvironmentDiagnosticReport?
  public private(set) var local: NetworkDiagnosticEnvironment?
  public private(set) var busy = false
  public private(set) var runState = NetworkDiagnosticRunState.idle
  public private(set) var progressReport: EnvironmentDiagnosticReport?
  public private(set) var stale = false
  public private(set) var message = "检测当前 Mac 和浏览器，与列表选中的线路无关"
  public private(set) var revision: UInt64 = 0
  private var task: Task<Void, Never>?
  private var generation: UInt64 = 0
  private var previous: (EnvironmentDiagnosticReport, NetworkDiagnosticEnvironment?, Bool)?
  private var activeSessionID: String?
  private let history: NetworkDiagnosticHistory?
  private let request: @Sendable (String, String) async throws -> EnvironmentDiagnosticReport
  private let openBrowser: @MainActor (URL) async -> Bool

  public init(
    history: NetworkDiagnosticHistory? = nil,
    request: @escaping @Sendable (String, String) async throws -> EnvironmentDiagnosticReport,
    openBrowser: @escaping @MainActor (URL) async -> Bool
  ) {
    self.history = history
    self.request = request
    self.openBrowser = openBrowser
    if let saved = history?.environment {
      report = saved.report
      previousReport = saved.previous
      local = saved.local
      stale = true
      message = "显示上次环境检测结果，请按需重新检测"
    }
  }
  public func start() {
    guard !busy else { return }
    generation &+= 1
    let token = generation
    previous = report.map { ($0, local, stale) }
    busy = true
    runState = .running
    progressReport = nil
    stale = report != nil
    message = "正在检测 Mac，等待浏览器采样；最长 45 秒"
    let sampledLocal = NetworkDiagnosticEnvironment.current()
    task = Task { [weak self, request] in
      var session = ""
      var acceptedSample = false
      do {
        let initial = try await request("start", "")
        session = initial.sessionID
        guard let self, generation == token, !Task.isCancelled else {
          _ = try? await request("cancel", session)
          return
        }
        activeSessionID = session
        progressReport = initial
        if previous == nil || initial.mac.complete || initial.browser.complete {
          report = initial
          local = sampledLocal
          acceptedSample = initial.mac.complete || initial.browser.complete
          revision &+= 1
          stale = false
        }
        let browserOpened: Bool
        if let url = URL(string: initial.browserURL), url.scheme == "http", url.host == "127.0.0.1"
        {
          browserOpened = await openBrowser(url)
        } else {
          browserOpened = false
        }
        guard generation == token, !Task.isCancelled else { throw CancellationError() }
        if !browserOpened { message = "无法打开浏览器，继续检测 Mac" }
        let deadline = ContinuousClock.now.advanced(by: .seconds(55))
        while ContinuousClock.now < deadline {
          try await Task.sleep(for: .seconds(1))
          let value = try await request("read", session)
          guard generation == token, !Task.isCancelled else { throw CancellationError() }
          progressReport = value
          if previous == nil || value.mac.complete || value.browser.complete {
            let changed = report != value
            report = value
            local = sampledLocal
            acceptedSample = value.mac.complete || value.browser.complete
            stale = false
            if changed { revision &+= 1 }
          }
          if value.complete || (!browserOpened && value.mac.complete) { break }
          message =
            !browserOpened
            ? "无法打开浏览器，正在检测 Mac…" : value.mac.complete ? "Mac 检测完成，等待浏览器结果…" : "正在检测 Mac 和浏览器…"
        }
        if acceptedSample, let report {
          runState = report.complete ? .finished : .failed
          if let old = previous?.0 { previousReport = old }
          history?.saveEnvironment(report, local: local, previous: previousReport)
          message =
            !browserOpened
            ? "Mac 检测完成；无法打开浏览器，浏览器检测未完成"
            : report.complete ? "检测完成；未取得的项目已注明原因" : "检测超时，保留已取得的结果"
        } else {
          runState = .failed
          restorePrevious()
          message = "本次未取得检测结果，保留上次结果"
        }
      } catch {
        if let self, generation == token, !Task.isCancelled {
          restorePrevious()
          message = ProductErrorPresentation.display(error)
          runState = .failed
        }
      }
      if !session.isEmpty { _ = try? await request("cancel", session) }
      guard let self, generation == token else { return }
      busy = false
      task = nil
      activeSessionID = nil
      previous = nil
      revision &+= 1
    }
  }
  public func cancel() {
    if busy { runState = .stopped }
    generation &+= 1
    task?.cancel()
    task = nil
    busy = false
    revision &+= 1
    if let id = activeSessionID, !id.isEmpty {
      Task { [request] in _ = try? await request("cancel", id) }
    }
    activeSessionID = nil
    restorePrevious()
    previous = nil
    message = "已停止检测，保留已取得的结果"
  }
  public func invalidate() {
    if busy { cancel() }
    if report != nil {
      stale = true
      message = "网络或实际连接已变化，以下为历史环境结果"
      revision &+= 1
    }
  }
  public func retainLastResult() {
    if busy { cancel() }
    if report != nil {
      stale = true
      message = "显示上次环境检测结果，请按需重新检测"
      revision &+= 1
    }
  }

  private func restorePrevious() {
    guard let previous else { return }
    (report, local, stale) = previous
    revision &+= 1
  }
  public func clear() {
    cancel()
    report = nil
    previousReport = nil
    local = nil
    stale = false
    runState = .idle
    progressReport = nil
    message = "检测当前 Mac 和浏览器，与列表选中的线路无关"
  }
}
