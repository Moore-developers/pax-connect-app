import Foundation
import PaxCore
import PaxUI

extension RoutingWorkbenchStore {
  convenience init(telemetry: TelemetryStore) {
    self.init(session: WorkbenchDemoStore(), backendAvailable: false, initialPeriod: .day)
    self.telemetry = telemetry
  }
  static func liveRow(_ row: PaxCore.TrafficRow, context: PresentationContext = .chinese)
    -> TrafficRow
  {
    var value = TrafficRow(
      id: row.id, name: row.name,
      subtitle: row.direction == "direct"
        ? context.text("traffic_details.b06325c566")
        : row.direction == "proxy"
          ? context.text("traffic_details.5e84ea61e8") : context.text("traffic_details.2e76742d3f"),
      symbol: "network", route: row.routeName,
      direction: row.direction == "direct" ? .direct : row.direction == "proxy" ? .proxy : .unknown,
      connections: [], upload: row.upload, download: row.download,
      establishment: workbenchLatencySamples(row))
    value.aggregateCount = Int(row.connections)
    value.aggregateBasis = TrafficRuleFormatting.latestTitle(row.latestMatch)
    value.latestLatency = TrafficFormatting.latestLatency(row)
    value.aggregatePeak = row.latencyKnown ? Double(row.slowestLatencyMS) : nil
    return value
  }
  var dataMessage: String? { dataMessage(context: .chinese) }
  func dataMessage(context: PresentationContext) -> String? {
    if let telemetry {
      if !liveError.isEmpty && (liveDataInvalid || livePage.items.isEmpty) {
        return context.text("traffic_details.a0a1a03a4b")
      }
      if liveDataInvalid || (liveLoading && livePage.items.isEmpty) {
        return context.text("traffic_details.b8735f7ddc")
      }
      return telemetry.trafficAvailable ? nil : context.text("traffic_details.37d4c165ac")
    }
    if !backendAvailable { return context.text("traffic_details.9ca94ce061") }
    switch session.scenario {
    case .loading: return context.text("traffic_details.b4b50b0fcd")
    case .trafficMissing: return context.text("traffic_details.8007bd0ca0")
    case .trafficFailed: return context.text("traffic_details.9372284a20")
    default: return nil
    }
  }
  var stateMessage: String { stateMessage(context: .chinese) }
  func stateMessage(context: PresentationContext) -> String {
    if let telemetry {
      return liveError.isEmpty
        ? telemetry.qualityMessage(context: context, includePastEviction: false)
        : liveErrorText(context: context)
    }
    if !backendAvailable { return context.text("traffic_details.9fe5b39e0d") }
    var messages: [String] = []
    if session.scenario == .disconnected {
      messages.append(context.text("traffic_details.c001e7639f") + TrafficFormatting.time(now))
    }
    if scene == .incomplete { messages.append(context.text("traffic_details.f73b1108b2")) }
    if !messages.isEmpty { return messages.joined(separator: "\n") }
    if session.scenario == .degraded { return context.text("traffic_details.09907f14c1") }
    return ""
  }
  func reloadLive(more: Bool = false, force: Bool = true, automatic: Bool = false) async {
    guard let telemetry, telemetry.trafficAvailable, tab == .traffic else { return }
    if more && (liveNeedsRead || livePage.readToken.isEmpty || livePage.nextCursor.isEmpty) {
      return
    }
    let rebuilding = liveNeedsRead
    let previous = livePage
    if rebuilding { pendingLivePage = nil }
    liveGeneration &+= 1
    let generation = liveGeneration
    liveLoading = true
    if rebuilding || previous.items.isEmpty { liveError = "" }
    let session = telemetry.sessionGeneration
    let invalidation = telemetry.trafficInvalidationGeneration
    let day = Calendar.current.startOfDay(for: Date())
    defer {
      if generation == liveGeneration {
        liveLoading = false
        let followUp = liveRefreshPending
        liveRefreshPending = false
        // PaxRefresh has already admitted this read. Finish the current request first,
        // then fulfill its one coalesced successor, including after a read failure.
        if followUp && !Task.isCancelled, session == telemetry.sessionGeneration,
          invalidation == telemetry.trafficInvalidationGeneration,
          telemetry.isShown(.traffic, focus: false), tab == .traffic
        {
          scheduleLive(force: true, automatic: true)
        }
      }
    }
    var filter = TrafficFilter()
    filter.limit = 15
    filter.dimension = ["application", "domain", "system"][dimension.rawValue]
    filter.direction = ["all", "proxy", "direct"][direction.rawValue]
    filter.window = period.window
    filter.search = search
    if more {
      filter.cursor = livePage.nextCursor
      filter.readToken = livePage.readToken
    }
    do {
      var page = try await telemetry.traffic(filter, force: force)
      guard !Task.isCancelled, generation == liveGeneration, session == telemetry.sessionGeneration,
        telemetry.connected, invalidation == telemetry.trafficInvalidationGeneration,
        period != .day || day == Calendar.current.startOfDay(for: Date())
      else { return }
      if more {
        guard previous.readToken == page.readToken, previous.asOf == page.asOf else {
          setLiveErrorKey("traffic_read.snapshot_changed")
          return
        }
        page.items = previous.items + page.items
        livePaginated = true
        liveFrozen = true
      } else if automatic && !rebuilding && livePaginated {
        // Keep the complete old token/cursor/count snapshot. Never splice new first-page rows into it.
        pendingLivePage = page
        liveError = ""
        return
      } else if automatic && !rebuilding && liveFrozen {
        // A single read contains at most 15 rows. If a displayed identity falls out
        // of it, retain the complete valid old snapshot rather than scan an unbounded
        // ranking or attach old values to a new token. Resume adopts this candidate.
        pendingLivePage = page
        let wanted = Set(previous.items.map(\.id))
        let latest = Dictionary(uniqueKeysWithValues: page.items.map { ($0.id, $0) })
        guard wanted.isSubset(of: Set(latest.keys)) else {
          liveError = ""
          return
        }
        page.items = previous.items.compactMap { latest[$0.id] }
        // Resume to the complete first page before allowing cursor pagination.
        page.nextCursor = ""
      } else {
        livePaginated = false
        pendingLivePage = nil
      }
      livePage = page
      liveError = ""
      now = page.asOf
    } catch {
      if !Task.isCancelled && generation == liveGeneration {
        setLiveError(ProductFailure(error))
      }
    }
  }
  func scheduleLive(
    delay: Bool = false, force: Bool = false, more: Bool = false, automatic: Bool = false
  ) {
    if automatic && liveLoading && !liveNeedsRead {
      liveRefreshPending = true
      return
    }
    liveRefreshPending = false
    liveQueryTask?.cancel()
    liveGeneration &+= 1
    liveQueryTask = Task { [weak self] in
      if delay { do { try await Task.sleep(for: .milliseconds(300)) } catch { return } }
      guard let self, self.telemetry?.isShown(.traffic, focus: false) == true else { return }
      await self.reloadLive(more: more, force: force, automatic: automatic)
    }
  }
  func resumeLiveStructure() {
    liveFrozen = false
    guard !livePaginated, !liveNeedsRead, let page = pendingLivePage else { return }
    livePage = page
    pendingLivePage = nil
  }
  func stopLive() {
    liveRefreshPending = false
    liveLoading = false
    liveQueryTask?.cancel()
    liveQueryTask = nil
    liveGeneration &+= 1
  }

}
