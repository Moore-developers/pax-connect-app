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
  private var messageValue = RetainedProductMessage(key: "diagnostic_state.9f9f17efdf")
  public private(set) var message: String {
    get { messageValue.original }
    set { messageValue = RetainedProductMessage(original: newValue) }
  }
  public func message(context: PresentationContext) -> String {
    messageValue.text(context: context)
  }
  public var isShowingHistoryMessage: Bool { messageValue.key == "diagnostic_state.427740ea8a" }
  public private(set) var revision: UInt64 = 0
  private var task: Task<Void, Never>?
  private var generation: UInt64 = 0
  private var previous: (EnvironmentDiagnosticReport, NetworkDiagnosticEnvironment?, Bool)?
  private var activeSessionID: String?
  private var scannedLocation: (token: UInt64, value: LocationExposure)?
  private let history: NetworkDiagnosticHistory?
  private let request:
    @Sendable (String, String, BrowserPresentation?) async throws -> EnvironmentDiagnosticReport
  private let browserPresentation: @MainActor () -> BrowserPresentation?
  private let openBrowser: @MainActor (URL) async -> Bool
  private let scanLocation: @Sendable () -> LocationExposure

  public init(
    history: NetworkDiagnosticHistory? = nil,
    request: @escaping @Sendable (String, String) async throws -> EnvironmentDiagnosticReport,
    openBrowser: @escaping @MainActor (URL) async -> Bool,
    scanLocation: @escaping @Sendable () -> LocationExposure = {
      LocationExposureScanner.current()
    },
    browserPresentation: @escaping @MainActor () -> BrowserPresentation? = { nil },
    requestWithPresentation: (
      @Sendable (String, String, BrowserPresentation?) async throws -> EnvironmentDiagnosticReport
    )? = nil
  ) {
    self.history = history
    self.scanLocation = scanLocation
    self.request =
      requestWithPresentation ?? { action, session, _ in try await request(action, session) }
    self.browserPresentation = browserPresentation
    self.openBrowser = openBrowser
    if let saved = history?.environment {
      report = saved.report
      previousReport = saved.previous
      local = saved.local
      stale = true
      messageValue = RetainedProductMessage(key: "diagnostic_state.427740ea8a")
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
    messageValue = RetainedProductMessage(key: "diagnostic_state.ea79df2570")
    let baseLocal = NetworkDiagnosticEnvironment.current()
    // File and system reads run off the main actor and merge in when they finish.
    scannedLocation = nil
    let scanLocation = scanLocation
    let scan = Task.detached(priority: .userInitiated) { scanLocation() }
    Task { [weak self] in
      let value = await scan.value
      guard let self, generation == token else { return }
      scannedLocation = (token, value)
      if busy, let current = local, current.at == baseLocal.at {
        local = current.withLocation(value)
        revision &+= 1
      }
    }
    let presentation = browserPresentation()
    task = Task { [weak self, request] in
      var session = ""
      var acceptedSample = false
      do {
        let initial = try await request("start", "", presentation)
        session = initial.sessionID
        guard let self, generation == token, !Task.isCancelled else {
          _ = try? await request("cancel", session, nil)
          return
        }
        activeSessionID = session
        progressReport = initial
        if previous == nil || initial.mac.complete || initial.browser.complete {
          report = initial
          local = sampledLocal(baseLocal, token)
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
        if !browserOpened {
          messageValue = RetainedProductMessage(key: "diagnostic_state.d3b4091f3f")
        }
        let deadline = ContinuousClock.now.advanced(by: .seconds(55))
        while ContinuousClock.now < deadline {
          try await Task.sleep(for: .seconds(1))
          let value = try await request("read", session, nil)
          guard generation == token, !Task.isCancelled else { throw CancellationError() }
          progressReport = value
          if previous == nil || value.mac.complete || value.browser.complete {
            let changed = report != value
            report = value
            local = sampledLocal(baseLocal, token)
            acceptedSample = value.mac.complete || value.browser.complete
            stale = false
            if changed { revision &+= 1 }
          }
          if value.complete || (!browserOpened && value.mac.complete) { break }
          messageValue =
            !browserOpened
            ? RetainedProductMessage(key: "diagnostic_state.a087cab3fd")
            : value.mac.complete
              ? RetainedProductMessage(key: "diagnostic_state.8eef58841d")
              : RetainedProductMessage(key: "diagnostic_state.f838cf9881")
        }
        if acceptedSample, let report {
          let scanned = await scan.value
          guard generation == token, !Task.isCancelled else { throw CancellationError() }
          if local != nil {
            scannedLocation = (token, scanned)
            local = local?.withLocation(scanned)
          }
          runState = report.complete ? .finished : .failed
          if let old = previous?.0 { previousReport = old }
          history?.saveEnvironment(report, local: local, previous: previousReport)
          messageValue =
            !browserOpened
            ? RetainedProductMessage(key: "diagnostic_state.344c4c08dc")
            : report.complete
              ? RetainedProductMessage(key: "diagnostic_state.2768a685c9")
              : RetainedProductMessage(key: "diagnostic_state.265ae74875")
        } else {
          runState = .failed
          restorePrevious()
          messageValue = RetainedProductMessage(key: "diagnostic_state.6cc20c1fe4")
        }
      } catch {
        if let self, generation == token, !Task.isCancelled {
          restorePrevious()
          messageValue = RetainedProductMessage(failure: ProductFailure(error))
          runState = .failed
        }
      }
      if !session.isEmpty { _ = try? await request("cancel", session, nil) }
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
      Task { [request] in _ = try? await request("cancel", id, nil) }
    }
    activeSessionID = nil
    restorePrevious()
    previous = nil
    messageValue = RetainedProductMessage(key: "diagnostic_state.c902c460d7")
  }
  public func invalidate() {
    if busy { cancel() }
    if report != nil {
      stale = true
      messageValue = RetainedProductMessage(key: "diagnostic_state.f3479ac290")
      revision &+= 1
    }
  }
  public func retainLastResult() {
    if busy { cancel() }
    if report != nil {
      stale = true
      messageValue = RetainedProductMessage(key: "diagnostic_state.427740ea8a")
      revision &+= 1
    }
  }

  private func sampledLocal(_ base: NetworkDiagnosticEnvironment, _ token: UInt64)
    -> NetworkDiagnosticEnvironment
  {
    base.withLocation(scannedLocation?.token == token ? scannedLocation?.value : nil)
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
    messageValue = RetainedProductMessage(key: "diagnostic_state.9f9f17efdf")
  }
}
