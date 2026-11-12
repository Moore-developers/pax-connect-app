import Foundation
import Observation

/// System environment is a local user snapshot, never sent to a diagnostic target.
public struct NetworkDiagnosticEnvironment: Sendable, Equatable, Codable {
  public let timezone: String
  public let languages: [String]
  public let region: String
  public let at: Date
  public init(timezone: String, languages: [String], region: String, at: Date) {
    self.timezone = timezone
    self.languages = languages
    self.region = region
    self.at = at
  }
  public static func current() -> Self {
    Self(
      timezone: TimeZone.current.identifier, languages: Locale.preferredLanguages,
      region: Locale.current.identifier, at: Date())
  }
  public func timezoneComparison(_ remote: String) -> String {
    guard let local = TimeZone(identifier: timezone), let other = TimeZone(identifier: remote)
    else {
      return "缺少有效 IP 时区资料，无法对照"
    }
    if local.identifier == other.identifier { return "系统时区与 IP 推定时区相同" }
    if local.secondsFromGMT(for: at) == other.secondsFromGMT(for: at) {
      return "时区标识不同，检测时刻 UTC 偏移相同"
    }
    return "系统时区与 IP 推定时区不同；先核对定位和预期设置"
  }
}

/// One window owns one cancelable measurement. Existing results stay historical
/// during a rerun; cancellation cannot commit late results or erase other modes.
@MainActor @Observable public final class NetworkDiagnosticStore {
  public let currentEnvironment: EnvironmentDiagnosticStore?
  public private(set) var report: NetworkDiagnosticReport?
  public private(set) var previousReport: NetworkDiagnosticReport?
  public private(set) var services: NetworkDiagnosticReport?
  public private(set) var environment: NetworkDiagnosticEnvironment?
  public private(set) var busy = false
  public private(set) var runState = NetworkDiagnosticRunState.idle
  public private(set) var progress: NetworkDiagnosticProgress?
  public private(set) var elapsedSeconds = 0
  public private(set) var progressError: String?
  public private(set) var startedAt: Date?
  public private(set) var message = "尚未检测；点击重新检测才会发出网络请求"
  public private(set) var stale = false
  public private(set) var generation: UInt64 = 0
  public var topic = 0
  public var onlyDifferences = false
  public var selectedSource: Int?
  public var preview = false
  public var expanded: Set<String> = []
  private let history: NetworkDiagnosticHistory?
  public var historyMessage: String? { history?.message }
  private let run: @Sendable (String, String) async throws -> NetworkDiagnosticReport
  private let readProgress: (@Sendable (String) async throws -> NetworkDiagnosticProgress?)?
  private var progressTask: Task<Void, Never>?
  private var task: Task<Void, Never>?
  private var deadlineTask: Task<Void, Never>?

  public init(
    currentEnvironment: EnvironmentDiagnosticStore? = nil,
    history: NetworkDiagnosticHistory? = nil,
    readProgress: (@Sendable (String) async throws -> NetworkDiagnosticProgress?)? = nil,
    run: @escaping @Sendable (String, String) async throws -> NetworkDiagnosticReport
  ) {
    self.currentEnvironment = currentEnvironment
    self.history = history
    self.run = run
    self.readProgress = readProgress
    if let previous = history?.route {
      report = previous.report
      services = previous.services
      previousReport = previous.previous
      environment = previous.environment
      stale = report != nil || services != nil
      if stale { message = "显示上次线路检测结果，请按需重新检测" }
    }
  }
  public static func sameBoundary(_ a: NetworkDiagnosticReport, _ b: NetworkDiagnosticReport)
    -> Bool
  {
    !a.changedDuringCapture && !b.changedDuringCapture && a.targetRouteID == b.targetRouteID
      && a.configRevision == b.configRevision && a.network == b.network
  }
  public var intelligenceReport: NetworkDiagnosticReport? {
    guard let report, !report.providerError.isEmpty, let previousReport,
      Self.sameBoundary(report, previousReport), let ip = report.exit?.ip, !ip.isEmpty,
      ip == previousReport.exit?.ip, previousReport.providerError.isEmpty
    else { return report }
    return previousReport
  }

  public func startAll() {
    guard !busy, currentEnvironment?.busy != true else { return }
    currentEnvironment?.start()
    start(mode: "full")
    let token = generation
    deadlineTask?.cancel()
    deadlineTask = Task { [weak self] in
      do { try await Task.sleep(for: .seconds(60)) } catch { return }
      guard let self, generation == token, busy || currentEnvironment?.busy == true else { return }
      cancel()
      currentEnvironment?.cancel()
      message = "整轮 60 秒预算耗尽；已停止在途检测并保留已有结果"
    }
  }

  public func start(mode: String = "full") {
    guard !busy else { return }
    generation &+= 1
    let token = generation
    busy = true
    runState = .running
    progress = nil
    progressError = nil
    elapsedSeconds = 0
    startedAt = Date()
    let operationID = UUID().uuidString
    followProgress(operationID, generation: token)
    if mode != "services", report != nil { stale = true }
    message = "正在经目标线路检测，最多 45 秒；已有结果为上次记录"
    let local = NetworkDiagnosticEnvironment.current()
    task = Task { [weak self, run] in
      do {
        let value = try await run(mode, operationID)
        guard let self, generation == token, !Task.isCancelled else { return }
        if mode == "full" || mode == "quick" || mode == "sources" {
          selectedSource = nil
          if let report, !report.changedDuringCapture,
            report.providerError.isEmpty || previousReport == nil
          {
            previousReport = report
          }
          if mode == "sources", let old = report, Self.sameBoundary(old, value),
            let ip = old.exit?.ip, !ip.isEmpty, ip == value.exit?.ip
          {
            report = value.preservingProbes(old.probes)
          } else {
            report = value
          }
          if mode != "sources" { environment = local }
          stale = value.changedDuringCapture
        } else {
          if let previous = report,
            previous.targetRouteID != value.targetRouteID
              || previous.configRevision != value.configRevision
              || previous.network != value.network
          {
            services = nil
            stale = true
            message = "服务检测目标与原报告不同，请重新进行快速检测"
            busy = false
            runState = .failed
            progressTask?.cancel()
            task = nil
            return
          }
          if !value.changedDuringCapture { services = value }
          if value.changedDuringCapture { stale = true }
        }
        message = value.changedDuringCapture ? "检测期间网络或线路已变化，本轮结果已失效" : "本轮已结束；资料、实测与未验证项分别标记"
        runState = value.changedDuringCapture ? .failed : .finished
        history?.saveRoute(
          report: report, services: services, environment: environment, previous: previousReport)
      } catch {
        guard let self, generation == token, !Task.isCancelled else { return }
        message = ProductErrorPresentation.display(error)
        runState = .failed
      }
      guard let self, generation == token else { return }
      busy = false
      if currentEnvironment?.busy != true {
        progressTask?.cancel()
        progressTask = nil
      }
      task = nil
    }
  }
  public func cancel() {
    if busy { runState = .stopped }
    generation &+= 1
    deadlineTask?.cancel()
    deadlineTask = nil
    task?.cancel()
    task = nil
    busy = false
    progressTask?.cancel()
    progressTask = nil
    message = "已停止检测；保留上次结果"
  }
  public func invalidate() {
    if busy { cancel() }
    if report != nil || services != nil {
      stale = true
      message = "网络或运行条件已变化，以下为历史结果，请重新检测"
    }
  }
  public func retainLastResult() {
    if busy { cancel() }
    if report != nil || services != nil {
      stale = true
      message = "显示上次线路检测结果，请按需重新检测"
    }
    currentEnvironment?.retainLastResult()
    preview = false
    expanded.removeAll()
  }
  public func showFailure(_ error: any Error) { message = ProductErrorPresentation.display(error) }
  public func clear() {
    currentEnvironment?.clear()
    cancel()
    report = nil
    previousReport = nil
    services = nil
    environment = nil
    stale = false
    runState = .idle
    progress = nil
    startedAt = nil
    message = "尚未检测；点击重新检测才会发出网络请求"
  }

  private func followProgress(_ operationID: String, generation token: UInt64) {
    progressTask?.cancel()
    progressTask = Task { [weak self, readProgress] in
      while !Task.isCancelled {
        guard let self, generation == token, busy || currentEnvironment?.busy == true else {
          return
        }
        elapsedSeconds = Int(Date().timeIntervalSince(startedAt ?? Date()))
        if busy, let readProgress {
          do {
            let value = try await readProgress(operationID)
            guard generation == token, busy, !Task.isCancelled else { return }
            if let value, value.operationID == operationID { progress = value }
            progressError = nil
          } catch {
            guard generation == token, busy, !Task.isCancelled else { return }
            progressError = "进度读取失败：" + ProductErrorPresentation.display(error)
          }
        }
        do { try await Task.sleep(for: .seconds(1)) } catch { return }
      }
    }
  }
}
