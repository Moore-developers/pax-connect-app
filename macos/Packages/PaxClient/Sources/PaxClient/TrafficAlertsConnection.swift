import Foundation
import GRPCCore
import PaxProto

extension DaemonConnection {
  public func trafficAlerts() async throws -> TrafficAlertState {
    try await historyRPC { service, options in
      TrafficAlertState(try await service.getTrafficAlerts(.init(), options: options))
    }
  }
  public func saveTrafficAlertSettings(_ value: TrafficAlertSettings) async throws
    -> TrafficAlertState
  {
    try await historyRPC { service, options in
      TrafficAlertState(try await service.saveTrafficAlertSettings(value.proto, options: options))
    }
  }
}

extension TrafficAlertSettings {
  init(_ v: Pax_V1_TrafficAlertSettings) {
    self.init()
    revision = v.revision
    enabled = v.enabled
    mediumSeconds = v.mediumSeconds
    let rule: (Pax_V1_TrafficAlertRule) -> TrafficAlertRule = {
      .init(enabled: $0.enabled, upload: $0.upload, download: $0.download)
    }
    short = rule(v.short)
    hour = rule(v.hour)
    twoHours = rule(v.twoHours)
    day = rule(v.day)
  }
  var proto: Pax_V1_TrafficAlertSettings {
    let rule: (TrafficAlertRule) -> Pax_V1_TrafficAlertRule = { r in
      .with {
        $0.enabled = r.enabled
        $0.upload = r.upload
        $0.download = r.download
      }
    }
    return .with {
      $0.revision = revision
      $0.enabled = enabled
      $0.mediumSeconds = mediumSeconds
      $0.short = rule(short)
      $0.hour = rule(hour)
      $0.twoHours = rule(twoHours)
      $0.day = rule(day)
    }
  }
}

extension TrafficAlertState {
  init(_ v: Pax_V1_TrafficAlertState) {
    self.init(
      settings: TrafficAlertSettings(v.settings), monitorState: v.monitorState,
      errorCode: v.errorCode,
      notices: v.notices.map {
        .init(
          id: $0.id, key: $0.key, source: $0.source, sourceKind: $0.sourceKind,
          direction: $0.direction, windowSeconds: $0.windowSeconds, bytes: $0.bytes,
          threshold: $0.threshold, until: $0.until.date, routeID: $0.routeID,
          routeName: $0.routeName, routeCount: $0.routeCount, partial: $0.partial)
      }, activeNoticeIDs: v.activeNoticeIds)
    revision = v.revision
  }
}
