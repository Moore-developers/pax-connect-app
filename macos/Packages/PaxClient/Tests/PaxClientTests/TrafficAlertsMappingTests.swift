import Foundation
import PaxProto
import Testing

@testable import PaxClient

struct TrafficAlertsMappingTests {
  @Test("提醒设置保留两套持续窗口阈值，通知首帧与增量一致")
  func wireMapping() {
    var value = TrafficAlertSettings()
    value.enabled = true
    value.mediumSeconds = 7_200
    value.short.upload = 1
    value.hour.upload = 2
    value.twoHours.upload = 3
    #expect(TrafficAlertSettings(value.proto) == value)
    let wire = Pax_V1_TrafficAlertState.with {
      $0.settings = value.proto
      $0.monitorState = "partial"
      $0.activeNoticeIds = ["notice"]
      $0.notices = [
        .with {
          $0.id = "notice"
          $0.key = "app:example:d"
          $0.source = "Example"
          $0.sourceKind = "app"
          $0.direction = "download"
          $0.windowSeconds = 300
          $0.bytes = 300_000_000
          $0.threshold = 250_000_000
          $0.until.seconds = 1_000
          $0.routeID = "a"
          $0.routeName = "A"
          $0.routeCount = 2
          $0.partial = true
        }
      ]
    }
    let snapshot = DaemonSnapshot(proto: .with { $0.trafficAlerts = wire })
    guard case .trafficAlerts(let event) = DaemonEvent(proto: .with { $0.trafficAlerts = wire })
    else {
      Issue.record("missing Watch mapping")
      return
    }
    #expect(snapshot.trafficAlerts == event)
    #expect(event.notices[0].routeCount == 2)
    #expect(event.notices[0].until == Date(timeIntervalSince1970: 1_000))
    #expect(event.notices[0].partial)
    #expect(event.activeNoticeIDs == ["notice"])
  }
}
