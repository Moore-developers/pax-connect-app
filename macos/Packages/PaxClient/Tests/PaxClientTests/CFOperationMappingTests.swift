import PaxProto
import Testing

@testable import PaxClient

@Suite("CF operation readback")
struct CFOperationMappingTests {
  @Test func failedValidationNeverInventsTokenOrCandidate() {
    var value = Pax_V1_OperationState()
    value.state = "failed"
    value.cfValidation.success = false
    guard case .cfValidation(let result) = DaemonOperationState(proto: value).networkResult else {
      Issue.record("Missing CF validation result")
      return
    }
    #expect(!result.success)
    #expect(result.candidate == nil)
    #expect(result.expiresAt == nil)
    #expect(result.validationID.isEmpty)
  }

  @Test func workspaceNetworksAndSavedConfigRemainSeparate() {
    var value = Pax_V1_OperationState()
    value.cfWorkspace.network = "recorded"
    value.cfWorkspace.currentNetwork = "current"
    guard case .cfWorkspace(let result) = DaemonOperationState(proto: value).networkResult else {
      Issue.record("Missing CF workspace result")
      return
    }
    #expect(result.network == "recorded")
    #expect(result.currentNetwork == "current")
    value.routeSave.routeID = "saved"
    value.routeSave.configRevision = 7
    let saved = DaemonOperationState(proto: value)
    #expect(saved.networkResult == nil)
    #expect(saved.savedConfigRevision == 7)
  }
}
