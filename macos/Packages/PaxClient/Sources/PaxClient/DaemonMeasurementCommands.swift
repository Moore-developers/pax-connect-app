import Foundation
import GRPCCore
import GRPCNIOTransportHTTP2Posix
import PaxProto

extension DaemonConnection {
  public func measurement(_ request: MeasurementRequest) async throws -> MeasurementReply {
    try await withAuthenticatedClient { client in
      var options = CallOptions.defaults
      options.timeout = .seconds(15)
      let rpc = Pax_V1_PaxService.Client(wrapping: client)
      do {
        switch request {
        case .refreshEgress:
          return .egress(
            PhysicalEgress(proto: try await rpc.getPhysicalEgress(.init(), options: options)))
        case .evaluate(let routes):
          let ack = try await rpc.evaluateMeasurementPreferences(
            .with {
              $0.targets = routes.map { route in
                .with {
                  $0.routeID = route.id
                  $0.expectedRouteRevision = route.revision
                  $0.expectedConfigRevision = route.configRevision
                }
              }
            }, options: options)
          return .operation(ack.operationID)
        case .networkPrivacy:
          return .networkPrivacy(
            NetworkPrivacy(proto: try await rpc.getNetworkPrivacy(.init(), options: options)))
        case .saveNetworkPrivacy(let value):
          return .networkPrivacy(
            NetworkPrivacy(proto: try await rpc.saveNetworkPrivacy(value.proto, options: options)))
        case .preferences:
          return .preferences(
            MeasurementPreferences(
              proto: try await rpc.getMeasurementPreferences(.init(), options: options)))
        case .savePreferences(let value):
          return .preferences(
            MeasurementPreferences(
              proto: try await rpc.saveMeasurementPreferences(value.proto, options: options)))
        case .cfWorkspace(let query):
          let result = try await rpc.queryCFWorkspace(
            .with {
              $0.network = query.network
              $0.routeID = query.routeID
              $0.expectedRevision = query.revision
              $0.documentJson = query.document
            }, options: options)
          return .workspace(CFWorkspace(result))
        case .cfTask(let action, let ips):
          let result = try await rpc.runCFTask(
            .with {
              $0.action = action
              $0.ips = ips
            }, options: options)
          return .operation(result.operationID)
        case .candidates:
          let result = try await rpc.queryCFCandidates(.init(), options: options)
          return .candidates(result.items.map(CFCandidate.init(proto:)))
        case .validateChainCF(
          let id, let revision, let document, let ip, let chain, let chainRevision):
          let result = try await rpc.validateCFCandidate(
            .with {
              $0.routeID = id
              $0.expectedRevision = revision
              $0.documentJson = document
              $0.ip = ip
              $0.chainID = chain
              $0.chainRevision = chainRevision
            }, options: options)
          return .candidate(CFCandidate(proto: result))
        case .validateCF(let id, let revision, let document, let ip):
          let result = try await rpc.validateCFCandidate(
            .with {
              $0.routeID = id
              $0.expectedRevision = revision
              $0.documentJson = document
              $0.ip = ip
            }, options: options)
          return .candidate(CFCandidate(proto: result))
        }
      } catch let error as RPCError {
        let codes: Set<String> = [
          "route_stale", "route_invalid", "cf_ip_invalid", "cf_unavailable", "network_changed",
          "physical_network_unavailable", "measurement_preferences_invalid",
          "measurement_preferences_stale", "measurement_preferences_save_failed",
          "assessment_unavailable", "download_busy", "cf_task_busy", "cf_workspace_unavailable",
          "network_privacy_invalid", "network_privacy_unavailable", "network_privacy_conflict",
          "network_privacy_save_failed",
          "cf_candidate_limit", "cf_cache_invalid", "cf_cache_save_failed",
        ]
        if codes.contains(error.message) { throw ProductError(code: error.message) }
        throw ClientError.classify(error)
      } catch { throw ClientError.classify(error) }
    }
  }

  public func trafficPathDiagnostics(recheck: Bool = false) async throws -> TrafficPathDiagnostics {
    try await withAuthenticatedClient { client in
      var options = CallOptions.defaults
      options.timeout = .seconds(18)
      do {
        var request = Pax_V1_ConnectionDiagnosticRequest()
        request.recheck = recheck
        let response = try await Pax_V1_PaxService.Client(wrapping: client)
          .diagnoseConnection(request, options: options)
        return TrafficPathDiagnostics(proto: response)
      } catch { throw ClientError.classify(error) }
    }
  }

}
