import Foundation
import GRPCCore
import GRPCNIOTransportHTTP2Posix
import PaxProto

extension DaemonConnection {
  private func usageRPC<T: Sendable>(
    timeout: Duration = .seconds(5),
    _ body:
      @Sendable (Pax_V1_AIUsageService.Client<HTTP2ClientTransport.Posix>, CallOptions) async throws
      -> T
  ) async throws -> T {
    do {
      return try await withAuthenticatedClient {
        client in
        var options = CallOptions.defaults
        options.timeout = timeout
        return try await body(Pax_V1_AIUsageService.Client(wrapping: client), options)
      }
    } catch let error as RPCError {
      if [
        "ai_usage_range_invalid", "ai_usage_unavailable", "ai_usage_busy", "ai_profile_not_found",
      ].contains(error.message) {
        throw ProductError(code: error.message)
      }
      throw ClientError.classify(error)
    } catch { throw ClientError.classify(error) }
  }
  public func aiUsageBuckets(from: Date, to: Date, granularity: String, revision: UInt64?)
    async throws -> AIUsageBucketPage
  {
    try await usageRPC { service, options in
      AIUsageBucketPage(
        try await service.queryAIUsageBuckets(
          .with {
            $0.fromMs = Int64(from.timeIntervalSince1970 * 1000)
            $0.toMs = Int64(to.timeIntervalSince1970 * 1000)
            $0.granularity = granularity
            if let revision { $0.knownRevision = revision }
          }, options: options))
    }
  }
  public func aiSessionAnalysis(
    from: Date, to: Date, sessions: [AISessionKey] = [], limitsOnly: Bool = false,
    comparison: Range<Date>? = nil
  ) async throws
    -> AISessionAnalysis
  {
    try await usageRPC { service, options in
      AISessionAnalysis(
        try await service.queryAIUsageAnalysis(
          .with {
            $0.fromMs = Int64(from.timeIntervalSince1970 * 1000)
            $0.toMs = Int64(to.timeIntervalSince1970 * 1000)
            $0.limitsOnly = limitsOnly
            if let comparison {
              $0.comparisonFromMs = Int64(comparison.lowerBound.timeIntervalSince1970 * 1000)
              $0.comparisonToMs = Int64(comparison.upperBound.timeIntervalSince1970 * 1000)
            }
            $0.sessions = sessions.map { key in
              .with {
                $0.cli = key.cli
                $0.sessionID = key.session
              }
            }
          }, options: options))
    }
  }
  public func aiSessionCatalog(
    from: Date, to: Date, cli: String, query: String, pageToken: String, selected: AISessionKey?
  )
    async throws -> AISessionCatalogPage
  {
    try await usageRPC { service, options in
      AISessionCatalogPage(
        try await service.listAISessions(
          .with {
            $0.fromMs = Int64(from.timeIntervalSince1970 * 1000)
            $0.toMs = Int64(to.timeIntervalSince1970 * 1000)
            $0.cli = cli
            $0.query = query
            $0.pageSize = 100
            $0.pageToken = pageToken
            if let selected {
              $0.selected = .with {
                $0.cli = selected.cli
                $0.sessionID = selected.session
              }
            }
          }, options: options))
    }
  }
  public func aiUsageSummary() async throws -> AIUsageBucketPage {
    try await usageRPC { service, options in
      AIUsageBucketPage(try await service.summarizeAIUsage(.init(), options: options))
    }
  }
  public func aiUsageLogs(from: Date, to: Date, filter: AIUsageFilter) async throws
    -> AIUsageLogPage
  {
    try await usageRPC { service, options in
      AIUsageLogPage(
        try await service.listAIUsageLogs(
          .with {
            $0.fromMs = Int64(from.timeIntervalSince1970 * 1000)
            $0.toMs = Int64(to.timeIntervalSince1970 * 1000)
            if let result = filter.result { $0.succeeded = result }
            if let minimum = filter.minOfficialCostMicro { $0.minOfficialCostMicro = minimum }
            if let maximum = filter.maxOfficialCostMicro { $0.maxOfficialCostMicro = maximum }
            $0.query = filter.query
            $0.profileID = filter.profile
            $0.modelID = filter.model
            $0.cli = filter.cli
            $0.page = UInt32(max(0, filter.page))
          }, options: options))
    }
  }
  /// Charges a profile's stored requests at its current multipliers (its model entry, else its
  /// default). A dry run changes nothing and returns the numbers a real run would. `model`
  /// limits the run to one model id.
  public func repriceAIUsage(
    profileID: String, model: String?, range: AIRepriceRange, dryRun: Bool
  ) async throws -> AIRepriceResult {
    try await usageRPC(timeout: .seconds(30)) { service, options in
      AIRepriceResult(
        try await service.repriceAIUsage(
          .with {
            $0.profileID = profileID
            if let model { $0.modelID = model }
            switch range {
            case .all: $0.all = true
            case .days(let from, let to):
              $0.fromDay = from
              $0.toDay = to
            }
            $0.dryRun = dryRun
          }, options: options))
    }
  }
}
