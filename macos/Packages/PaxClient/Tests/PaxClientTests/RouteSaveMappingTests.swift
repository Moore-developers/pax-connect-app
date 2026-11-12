import PaxProto
import Testing

@testable import PaxClient

struct RouteSaveMappingTests {
  @Test func optionalSaveOutcome() {
    var wire = Pax_V1_OperationResult()
    wire.operationID = "save"
    wire.ok = true
    #expect(OperationResult(proto: wire).routeSave == nil)
    wire.routeSave.routeID = "route"
    wire.routeSave.created = false
    #expect(
      OperationResult(proto: wire).routeSave == RouteSaveResult(routeID: "route", created: false))
    wire.routeSave.created = true
    #expect(OperationResult(proto: wire).routeSave?.created == true)
  }
}
