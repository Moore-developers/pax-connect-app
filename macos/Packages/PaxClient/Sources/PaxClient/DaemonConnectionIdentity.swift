import GRPCCore
import GRPCNIOTransportHTTP2Posix
import PaxProto

// Hello identity is checked before reading a snapshot or confirming a Watch.
extension DaemonConnection {
  /// 握手：协议版本不一致时抛 `ClientError.protocolMismatch`，不静默降级。
  public func handshake() async throws -> DaemonHandshake {
    try await withGRPCClient(transport: try Self.makeTransport(socketPath: socketPath)) { client in
      var options = CallOptions.defaults
      options.timeout = .seconds(5)
      let response = try await Pax_V1_PaxService.Client(wrapping: client).hello(
        .with {
          $0.clientVersion = self.clientVersion
          $0.protocolVersion = Self.protocolVersion
        }, options: options
      )
      try self.validateIdentity(response)
      return DaemonHandshake(
        daemonVersion: response.daemonVersion,
        protocolVersion: response.protocolVersion,
        snapshotSeq: response.snapshotSeq, serviceFingerprint: response.serviceFingerprint,
        capabilities: Set(response.capabilities), initialInstall: response.initialInstall,
        ownerAppPID: response.ownerAppPid
      )
    }
  }

  /// Every short-lived RPC channel verifies the owner before sending product data.
  /// This also protects shutdown after an initial launch/identity failure.
  func withAuthenticatedClient<Result: Sendable>(
    _ body: (GRPCClient<HTTP2ClientTransport.Posix>) async throws -> Result
  ) async throws -> Result {
    try await withGRPCClient(transport: try Self.makeTransport(socketPath: socketPath)) { client in
      if expectedOwnerPID != nil {
        var options = CallOptions.defaults
        options.timeout = .seconds(5)
        let hello = try await Pax_V1_PaxService.Client(wrapping: client).hello(
          .with {
            $0.clientVersion = self.clientVersion
            $0.protocolVersion = Self.protocolVersion
          }, options: options)
        try self.validateIdentity(hello)
      }
      return try await body(client)
    }
  }

  func validateIdentity(_ hello: Pax_V1_HelloResponse) throws {
    try validate(protocolVersion: hello.protocolVersion)
    if let expectedOwnerPID {
      guard hello.ownerAppPid == expectedOwnerPID,
        hello.capabilities.contains("paxd.user"),
        let expectedFingerprint, !expectedFingerprint.isEmpty,
        hello.serviceFingerprint == expectedFingerprint
      else { throw ClientError.serviceIdentityUnknown }
    }
  }

  func validate(protocolVersion: UInt32) throws {
    guard protocolVersion == Self.protocolVersion else {
      throw ClientError.protocolMismatch(daemon: protocolVersion, client: Self.protocolVersion)
    }
  }

}
