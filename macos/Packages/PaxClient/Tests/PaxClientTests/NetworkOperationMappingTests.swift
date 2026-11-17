import PaxProto
import Testing

@testable import PaxClient

@Suite("Network operation readback")
struct NetworkOperationMappingTests {
  @Test func unknownProgressDoesNotBecomeZero() {
    var value = Pax_V1_OperationState()
    value.state = "running"
    value.progress.phase = "queued"
    let result = DaemonOperationState(proto: value)
    #expect(result.progress?.completed == nil)
    #expect(result.progress?.total == nil)
    #expect(result.networkResult == nil)
    value.progress.completed = 0
    #expect(DaemonOperationState(proto: value).progress?.completed == 0)
  }

  @Test func selectedAndRunningRemainDistinct() {
    var value = Pax_V1_OperationState()
    value.proxy.status = .degraded
    value.proxy.selectedRouteID = "requested"
    value.proxy.runningRouteID = "actual"
    let result = DaemonOperationState(proto: value)
    guard case .proxy(let status, let selected, let running, _) = result.networkResult else {
      Issue.record("Missing typed proxy result")
      return
    }
    #expect(status == .degraded)
    #expect(selected == "requested")
    #expect(running == "actual")
    #expect(result.savedRouteID == nil)
  }

  @Test func batchKeepsFailedItemAndMeasurementKind() {
    var value = Pax_V1_OperationState()
    value.measurements.items = [
      .with {
        $0.routeID = "failed-route"
        $0.configRevision = 7
        $0.error.code = "download_timeout"
        $0.download.error.code = "download_timeout"
      }
    ]
    guard case .measurements(let items) = DaemonOperationState(proto: value).networkResult else {
      Issue.record("Missing typed measurements")
      return
    }
    #expect(items.count == 1)
    #expect(items[0].error?.code == "download_timeout")
    #expect(items[0].download != nil)
    #expect(items[0].latency == nil)
    #expect(items[0].exit == nil)
  }
}
