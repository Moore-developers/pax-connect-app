import Foundation
import Testing

@testable import PaxCore

@MainActor @Suite("当前连接事件") struct ConnectionJournalTests {
  @Test("迟到的旧线路事件不进入当前代次，异常筛选不混入成功")
  func generationFilter() {
    func log(_ route: String, _ generation: UInt64, _ code: String = "") -> ProductLog {
      .init(
        at: Date(), level: .info, message: "event", operationID: "", snapshotVersion: 1,
        connection: .init(
          session: "A", generation: generation, routeID: route, connectionID: "id",
          target: "example.com:443", stage: "dial", code: code))
    }
    let rows = [log("old", 1, "timeout"), log("new", 2), log("new", 2, "refused")]
    #expect(
      ConnectionJournalPresentation.current(
        rows, session: "A", generation: 2, routeID: "new", errorsOnly: false
      ).count == 2)
    #expect(
      ConnectionJournalPresentation.current(
        rows, session: "A", generation: 2, routeID: "new", errorsOnly: true
      ).count == 1)
    #expect(
      ConnectionJournalPresentation.current(
        rows, session: "B", generation: 2, routeID: "new", errorsOnly: false
      ).isEmpty)
  }
}
