import Foundation
import Observation

/// System environment is a local user snapshot, never sent to a diagnostic target.
public struct NetworkDiagnosticEnvironment: Sendable, Equatable, Codable {
  public let timezone: String
  public let languages: [String]
  public let region: String
  public let at: Date
  /// nil in records saved before the location scan existed.
  public let location: LocationExposure?
  public init(
    timezone: String, languages: [String], region: String, at: Date,
    location: LocationExposure? = nil
  ) {
    self.timezone = timezone
    self.languages = languages
    self.region = region
    self.at = at
    self.location = location
  }
  public func withLocation(_ location: LocationExposure?) -> Self {
    Self(timezone: timezone, languages: languages, region: region, at: at, location: location)
  }
  public static func current() -> Self {
    Self(
      timezone: TimeZone.current.identifier, languages: Locale.preferredLanguages,
      region: Locale.current.identifier, at: Date())
  }
  public func timezoneComparison(_ remote: String, context: PresentationContext = .chinese)
    -> String
  {
    guard let local = TimeZone(identifier: timezone), let other = TimeZone(identifier: remote)
    else {
      return context.text("diagnostic_state.239819bd20")
    }
    if local.identifier == other.identifier { return context.text("diagnostic_state.4361092a47") }
    if local.secondsFromGMT(for: at) == other.secondsFromGMT(for: at) {
      return context.text("diagnostic_state.3f117a942d")
    }
    return context.text("diagnostic_state.5075976f12")
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
  private var progressErrorValue: RetainedProductMessage?
  public private(set) var progressError: String? {
    get { progressErrorValue?.original }
    set { progressErrorValue = newValue.map { RetainedProductMessage(original: $0) } }
  }
  public func progressError(context: PresentationContext) -> String? {
    progressErrorValue?.text(context: context)
  }
  public private(set) var startedAt: Date?
  private var messageValue = RetainedProductMessage(key: "diagnostic_state.7f2694a199")
  public private(set) var message: String {
    get { messageValue.original }
    set { messageValue = RetainedProductMessage(original: newValue) }
  }
  public func message(context: PresentationContext) -> String {
    messageValue.text(context: context)
  }
  public var isShowingHistoryMessage: Bool { messageValue.key == "diagnostic_state.d226731dec" }
  public private(set) var stale = false
  public private(set) var generation: UInt64 = 0
  public var topic = 0
  public var onlyDifferences = false
  public var selectedSource: Int?
  public var expanded: Set<String> = []
  private let history: NetworkDiagnosticHistory?
  public var historyMessage: String? { history?.message }
  public func historyMessage(context: PresentationContext) -> String? {
    history?.message(context: context)
  }
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
      if stale { messageValue = RetainedProductMessage(key: "diagnostic_state.d226731dec") }
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
      messageValue = RetainedProductMessage(key: "diagnostic_state.f795d5bb02")
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
    messageValue = RetainedProductMessage(key: "diagnostic_state.11cfb999c4")
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
            messageValue = RetainedProductMessage(key: "diagnostic_state.d1b316128a")
            busy = false
            runState = .failed
            progressTask?.cancel()
            task = nil
            return
          }
          if !value.changedDuringCapture { services = value }
          if value.changedDuringCapture { stale = true }
        }
        messageValue =
          value.changedDuringCapture
          ? RetainedProductMessage(key: "diagnostic_state.aecdcf2c6c")
          : RetainedProductMessage(key: "diagnostic_state.5c8150e6db")
        runState = value.changedDuringCapture ? .failed : .finished
        history?.saveRoute(
          report: report, services: services, environment: environment, previous: previousReport)
      } catch {
        guard let self, generation == token, !Task.isCancelled else { return }
        messageValue = RetainedProductMessage(failure: ProductFailure(error))
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
    messageValue = RetainedProductMessage(key: "diagnostic_state.5713061b59")
  }
  public func invalidate() {
    if busy { cancel() }
    if report != nil || services != nil {
      stale = true
      messageValue = RetainedProductMessage(key: "diagnostic_state.25d85db49c")
    }
  }
  public func retainLastResult() {
    if busy { cancel() }
    if report != nil || services != nil {
      stale = true
      messageValue = RetainedProductMessage(key: "diagnostic_state.d226731dec")
    }
    currentEnvironment?.retainLastResult()
    expanded.removeAll()
  }
  public func showFailure(_ error: any Error) {
    messageValue = RetainedProductMessage(failure: ProductFailure(error))
  }
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
    messageValue = RetainedProductMessage(key: "diagnostic_state.7f2694a199")
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
            progressErrorValue = RetainedProductMessage(
              key: "diagnostic.progress_failure", failure: ProductFailure(error))
          }
        }
        do { try await Task.sleep(for: .seconds(1)) } catch { return }
      }
    }
  }
}
