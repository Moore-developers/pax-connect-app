import Foundation
import GRPCCore
import GRPCNIOTransportHTTP2Posix
import PaxProto

extension DaemonConnection {
  func historyRPC<T: Sendable>(
    _ body:
      @Sendable (Pax_V1_PaxService.Client<HTTP2ClientTransport.Posix>, CallOptions) async throws ->
      T
  ) async throws -> T {
    do {
      return try await withAuthenticatedClient {
        client in
        var options = CallOptions.defaults
        options.timeout = .seconds(5)
        return try await body(Pax_V1_PaxService.Client(wrapping: client), options)
      }
    } catch let error as RPCError {
      if error.message.hasPrefix("history_") || error.message.hasPrefix("icon_")
        || error.message.hasPrefix("routing_") || error.message.hasPrefix("fetch_")
        || ["invalid_filter", "settings_conflict", "operation_expired"].contains(error.message)
      {
        throw ProductError(code: error.message)
      }
      throw ClientError.classify(error)
    } catch { throw ClientError.classify(error) }
  }
  public func logUpdates(_ q: LogUpdateRequest) async throws -> LogUpdatePage {
    try await historyRPC { service, options in
      LogUpdatePage(
        try await service.queryLogUpdates(
          .with {
            $0.filter = Self.logRequest(q.filter)
            $0.afterSequence = q.afterSequence
            $0.deletionGeneration = q.deletionGeneration
            $0.journalGeneration = q.journalGeneration
            $0.countOnly = q.countOnly
            $0.limit = q.limit
          }, options: options))
    }
  }
  public func recheckCurrentPath(_ requestID: String) async throws -> String {
    try await historyRPC { service, options in
      try await service.recheckCurrentPath(.with { $0.requestID = requestID }, options: options)
        .operationID
    }
  }
  public func queryLogs(_ q: LogFilter) async throws -> HistoryLogPage {
    try await historyRPC { service, options in
      HistoryLogPage(try await service.queryLogs(Self.logRequest(q), options: options))
    }
  }
  public func queryLogObjects(_ q: LogFilter) async throws -> HistoryObjectPage {
    try await historyRPC { service, options in
      HistoryObjectPage(try await service.queryLogObjects(Self.logRequest(q), options: options))
    }
  }
  public func logRecordDetail(_ q: LogDetailFilter) async throws -> HistoryLogDetail {
    try await historyRPC { service, options in
      HistoryLogDetail(
        try await service.getLogDetail(
          .with {
            $0.recordID = q.recordID
            $0.query = Self.logRequest(q.query)
          }, options: options))
    }
  }
  public func logDetail(_ id: String) async throws -> HistoryLogDetail {
    try await historyRPC { service, options in
      HistoryLogDetail(try await service.getLogDetail(.with { $0.eventID = id }, options: options))
    }
  }
  public func queryTraffic(_ q: TrafficFilter) async throws -> TrafficPage {
    try await historyRPC { service, options in
      TrafficPage(
        try await service.queryTraffic(
          .with {
            $0.dimension = q.dimension
            $0.direction = q.direction
            $0.window = q.window
            if let until = q.until {
              $0.until.seconds = Int64(until.timeIntervalSince1970)
              $0.until.nanos = Int32(
                (until.timeIntervalSince1970 - floor(until.timeIntervalSince1970)) * 1_000_000_000)
            }
            $0.search = q.search
            $0.readToken = q.readToken
            $0.cursor = q.cursor
            $0.limit = q.limit
          }, options: options))
    }
  }
  public func trafficDetail(_ q: TrafficDetailQuery) async throws -> TrafficDetail {
    try await historyRPC { service, options in
      TrafficDetail(
        try await service.queryTrafficDetail(
          .with {
            $0.dnsGroupID = q.dnsGroupID
            $0.rowID = q.rowID
            $0.connectionKey = q.connectionKey
            $0.readToken = q.readToken
            $0.cursor = q.cursor
            $0.limit = q.limit
          }, options: options))
    }
  }
  public func clearLogs(_ id: String, at: Date) async throws -> String {
    try await historyRPC { service, options in
      try await service.clearLogs(
        .with {
          $0.operationID = id
          $0.requestedAt.seconds = Int64(at.timeIntervalSince1970)
        }, options: options
      ).operationID
    }
  }
  public func saveLogRetention(_ hours: UInt32, revision: UInt64, id: String, at: Date) async throws
    -> String
  {
    try await historyRPC { service, options in
      try await service.updateSettings(
        .with {
          $0.settings.logRetentionHours = hours
          $0.fieldMask = ["log_retention_hours"]
          $0.expectedRevision = revision
          $0.operationID = id
          $0.requestedAt.seconds = Int64(at.timeIntervalSince1970)
        }, options: options
      ).operationID
    }
  }
  static func logRequest(_ q: LogFilter) -> Pax_V1_LogQuery {
    .with {
      $0.scope = q.scope
      $0.quick = q.quick
      $0.criteria = .with {
        $0.kinds = q.criteria.kinds
        $0.levels = q.criteria.levels
        $0.application = q.criteria.application
        $0.domain = q.criteria.domain
        $0.domainMode = q.criteria.domainMode
        $0.targetIp = q.criteria.targetIP
        $0.transports = q.criteria.transports
        $0.port = q.criteria.port
        $0.directions = q.criteria.directions
        $0.route = q.criteria.route
        $0.rulePackage = q.criteria.rulePackage
        $0.minLatencyMs = q.criteria.minLatencyMS
        $0.dnsAnswerIp = q.criteria.dnsAnswerIP
      }
      $0.includeDns = q.includeDNS
      $0.view = q.view
      $0.problemID = q.problemID
      $0.connectionKey = q.connectionKey
      $0.dimension = q.dimension
      $0.objectID = q.objectID
      $0.search = q.search
      $0.level = q.level
      $0.routeID = q.routeID
      $0.readToken = q.readToken
      $0.cursor = q.cursor
      $0.limit = q.limit
      if let from = q.from { $0.from.seconds = Int64(from.timeIntervalSince1970) }
      if let until = q.until { $0.until.seconds = Int64(until.timeIntervalSince1970) }
    }
  }
}

extension DaemonConnection {
  public func objectIcon(host: String, fetch: Bool, generation: UInt64) async throws
    -> ObjectIconResult
  {
    try await historyRPC { service, options in
      let response = try await service.queryObjectIcon(
        .with {
          $0.host = host
          $0.fetch = fetch
          $0.logDeletionGeneration = generation
        }, options: options)
      var value = ObjectIconResult()
      value.host = response.host
      value.png = response.png
      value.state = response.state
      value.reason = response.reason
      value.expiresAt = response.hasExpiresAt ? response.expiresAt.date : nil
      value.logDeletionGeneration = response.logDeletionGeneration
      return value
    }
  }
}

extension DaemonConnection {
  public func routingRules() async throws -> RoutingRulesSnapshot {
    try await historyRPC { service, options in
      RoutingRulesSnapshot(try await service.queryRoutingRules(.init(), options: options))
    }
  }
}
