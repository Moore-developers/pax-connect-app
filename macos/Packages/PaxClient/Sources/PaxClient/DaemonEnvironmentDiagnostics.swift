import Foundation
import GRPCCore
import PaxProto

extension DaemonConnection {
  public func inspectEnvironment(action: String, sessionID: String) async throws
    -> EnvironmentDiagnosticReport
  {
    try await withAuthenticatedClient { client in
      var options = CallOptions.defaults
      options.timeout = .seconds(action == "sources" ? 23 : 5)
      do {
        let value = try await Pax_V1_PaxService.Client(wrapping: client).inspectEnvironment(
          .with {
            $0.action = action
            $0.sessionID = sessionID
          }, options: options)
        return EnvironmentDiagnosticReport(value)
      } catch { throw ClientError.classify(error) }
    }
  }
}
