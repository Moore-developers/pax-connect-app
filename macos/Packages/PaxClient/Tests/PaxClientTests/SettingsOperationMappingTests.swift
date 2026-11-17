import PaxProto
import Testing

@testable import PaxClient

@Suite("Settings operation readback")
struct SettingsOperationMappingTests {
  @Test func frozenOwnerValuesPreserveZeroAndAbsentTime() {
    var value = Pax_V1_OperationState()
    value.settings.retention.revision = 0
    value.settings.retention.retentionHours = 24
    value.settings.retention.cleanupState = "completed"
    #expect(
      DaemonOperationState(proto: value).settingsResult
        == .retention(revision: 0, hours: 24, cleanupState: "completed"))
    value.settings.clear.clearGeneration_p = 3
    value.settings.clear.logsRevision = 7
    #expect(
      DaemonOperationState(proto: value).settingsResult
        == .clear(requestedAt: nil, clearGeneration: 3, logsRevision: 7))
    value.settings.privacy.revision = 4
    value.settings.privacy.dnsMode = "strict"
    value.settings.privacy.webrtc = false
    guard case .privacy(let privacy) = DaemonOperationState(proto: value).settingsResult else {
      Issue.record("Missing privacy result")
      return
    }
    #expect(privacy.revision == 4)
    #expect(!privacy.webRTC)
    value.settings.measurement.latencyParallel = 24
    guard case .measurement(let measurement) = DaemonOperationState(proto: value).settingsResult
    else {
      Issue.record("Missing measurement result")
      return
    }
    #expect(measurement.latency == 24)
    #expect(DaemonOperationState(proto: .init()).settingsResult == nil)
  }
}
