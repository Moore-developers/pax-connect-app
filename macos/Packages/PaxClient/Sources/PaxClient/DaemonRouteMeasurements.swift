import Foundation
import GRPCCore
import PaxProto

extension DaemonConnection {
  /// 对一条已保存线路做一次有界实测：无 TUN、不选路、不改选定与当前线路。
  public func testRoute(
    _ route: NodeProfile, download: Bool = false, diagnostic: Bool = false, exit: Bool = false
  )
    async throws -> String
  {
    try await submitTests(
      [route], download: download, batch: false, diagnostic: diagnostic, exit: exit)
  }

  public func testRoutes(_ routes: [NodeProfile], download: Bool, exit: Bool = false) async throws
    -> String
  {
    try await submitTests(routes, download: download, batch: true, exit: exit)
  }

  private func submitTests(
    _ routes: [NodeProfile], download: Bool, batch: Bool, diagnostic: Bool = false,
    exit: Bool = false
  ) async throws
    -> String
  {
    try await withAuthenticatedClient { client in
      var options = CallOptions.defaults
      options.timeout = .seconds(5)
      do {
        let ack = try await Pax_V1_PaxService.Client(wrapping: client).testRoute(
          .with {
            if batch {
              $0.targets = routes.map { route in
                .with {
                  $0.routeID = route.id
                  $0.expectedRouteRevision = route.revision
                  $0.expectedConfigRevision = route.configRevision
                }
              }
            } else if let route = routes.first {
              $0.routeID = route.id
              $0.expectedRouteRevision = route.revision
            }
            $0.clientID = self.clientID
            $0.requestID = UUID().uuidString
            $0.kind = exit ? "exit" : download ? "download" : diagnostic ? "diagnostic" : "latency"
          }, options: options)
        return ack.operationID
      } catch let error as RPCError {
        let known: Set<String> = [
          "download_busy", "download_unavailable", "exit_unavailable", "route_stale",
          "route_not_found",
          "tls_confirmation_required", "test_unavailable", "test_batch_invalid", "operation_busy",
        ]
        if known.contains(error.message) { throw ProductError(code: error.message) }
        throw ClientError.classify(error)
      } catch { throw ClientError.classify(error) }
    }
  }

}
