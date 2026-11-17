import Foundation
import PaxProto

/// Settings facts frozen by the original owner; reading never reapplies them.
public enum DaemonSettingsOperation: Sendable, Equatable {
  case privacy(NetworkPrivacy)
  case measurement(MeasurementPreferences)
  case retention(revision: UInt64, hours: UInt32, cleanupState: String)
  case clear(requestedAt: Date?, clearGeneration: UInt64, logsRevision: UInt64)

  init?(_ value: Pax_V1_OperationState) {
    guard case .settings(let result) = value.result else { return nil }
    switch result.value {
    case .privacy(let privacy):
      self = .privacy(NetworkPrivacy(proto: privacy))
    case .measurement(let measurement):
      self = .measurement(MeasurementPreferences(proto: measurement))
    case .retention(let retention):
      self = .retention(
        revision: retention.revision, hours: retention.retentionHours,
        cleanupState: retention.cleanupState)
    case .clear(let clear):
      self = .clear(
        requestedAt: clear.hasRequestedAt ? clear.requestedAt.date : nil,
        clearGeneration: clear.clearGeneration_p, logsRevision: clear.logsRevision)
    case nil:
      return nil
    }
  }
}
