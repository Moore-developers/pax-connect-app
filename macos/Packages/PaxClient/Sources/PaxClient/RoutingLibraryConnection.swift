import Foundation
import GRPCCore
import PaxProto

extension DaemonConnection {
  public func geoRules(search: String, offset: UInt32) async throws -> GeoRulesPage {
    try await historyRPC { service, options in
      GeoRulesPage(
        try await service.queryGeoRules(
          .with {
            $0.search = search
            $0.offset = offset
            $0.limit = 100
          }, options: options))
    }
  }
  public func routingLibrary() async throws -> RoutingLibrarySnapshot {
    try await historyRPC { service, options in
      var configured = options
      configured.timeout = .seconds(15)
      return RoutingLibrarySnapshot(
        try await service.queryRoutingLibrary(.init(), options: configured))
    }
  }
  public func mutateRoutingLibrary(_ command: RoutingMutation) async throws
    -> RoutingLibrarySnapshot
  {
    try await historyRPC { service, options in
      var configured = options
      configured.timeout = command.action == "update_geo" ? .seconds(60) : .seconds(35)
      return RoutingLibrarySnapshot(
        try await service.mutateRoutingLibrary(command.proto, options: configured)
      )
    }
  }
}
