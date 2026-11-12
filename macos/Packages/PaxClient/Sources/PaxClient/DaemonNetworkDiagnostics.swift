import Foundation
import GRPCCore
import PaxProto

extension DaemonConnection {
  public func diagnoseNetwork(mode: String, operationID: String = "") async throws
    -> NetworkDiagnosticReport
  {
    NetworkDiagnosticReport(
      try await diagnosticRequest(
        .with {
          $0.mode = mode
          $0.operationID = operationID
        }, progress: false))
  }

  public func networkDiagnosticProgress(operationID: String) async throws
    -> NetworkDiagnosticProgress?
  {
    let value = try await diagnosticRequest(
      .with {
        $0.operationID = operationID
        $0.readProgress = true
      }, progress: true)
    return value.hasProgress ? NetworkDiagnosticProgress(value.progress) : nil
  }

  private func diagnosticRequest(_ request: Pax_V1_NetworkDiagnosticRequest, progress: Bool)
    async throws -> Pax_V1_NetworkDiagnosticReport
  {
    try await withAuthenticatedClient { client in
      var options = CallOptions.defaults
      options.timeout = .seconds(progress ? 3 : 49)
      do {
        return try await Pax_V1_PaxService.Client(wrapping: client)
          .diagnoseNetwork(request, options: options)
      } catch let error as RPCError {
        if progress && error.code == .notFound && error.message == "diagnostic_progress_pending" {
          return .init()
        }
        if [
          "download_busy", "measurement_busy", "path_diagnostics_busy",
          "physical_network_unavailable", "route_not_selected", "tls_confirmation_required",
        ].contains(error.message) {
          throw ProductError(code: error.message)
        }
        throw ClientError.classify(error)
      } catch { throw ClientError.classify(error) }
    }
  }
}
