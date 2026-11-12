import PaxProto
import Testing

@testable import PaxClient

@Suite("后台恢复身份")
struct BackendIdentityTests {
  @Test("仅接受当前App归属、当前指纹及用户后台能力；协议先校验")
  func currentAppIdentity() async throws {
    let client = DaemonConnection(
      socketPath: "/unused", expectedOwnerPID: 42, expectedFingerprint: "current")
    var hello = Pax_V1_HelloResponse()
    hello.protocolVersion = DaemonConnection.protocolVersion
    hello.ownerAppPid = 42
    hello.serviceFingerprint = "current"
    hello.capabilities = ["paxd.user"]
    try await client.validateIdentity(hello)
    hello.ownerAppPid = 99
    await #expect(throws: ClientError.serviceIdentityUnknown) {
      try await client.validateIdentity(hello)
    }
    hello.ownerAppPid = 42
    hello.serviceFingerprint = "old-bundle"
    await #expect(throws: ClientError.serviceIdentityUnknown) {
      try await client.validateIdentity(hello)
    }
    hello.serviceFingerprint = "current"
    hello.capabilities = []
    await #expect(throws: ClientError.serviceIdentityUnknown) {
      try await client.validateIdentity(hello)
    }
    hello.protocolVersion = 1
    await #expect(
      throws: ClientError.protocolMismatch(daemon: 1, client: DaemonConnection.protocolVersion)
    ) { try await client.validateIdentity(hello) }
  }
}
