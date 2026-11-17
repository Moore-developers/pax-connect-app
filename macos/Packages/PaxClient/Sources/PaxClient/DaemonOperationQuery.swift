import Foundation
import GRPCCore
import PaxProto

/// A readback fact; unknown does not imply that a mutation failed or rolled back.
public struct DaemonOperationState: Sendable, Equatable {
  public let daemonInstance: String
  public let operationID: String
  public let requestID: String
  public let ownerClientID: String
  public let kind: String
  public let state: String
  public let cancellable: Bool
  public let startedAt: Date?
  public let finishedAt: Date?
  public let unknownReason: String
  public let error: ProductError?
  public let savedRouteID: String?
  public let catalogRevision: UInt64?
  public let savedRouteRevision: UInt64?
  public let savedConfigRevision: UInt64?
  public let settingsResult: DaemonSettingsOperation?
  public let profileResult: DaemonAIProfileOperation?
  public let networkResult: DaemonNetworkOperation?
  public let progress: DaemonOperationProgress?
  public let affectedTarget: String

  init(proto: Pax_V1_OperationState) {
    daemonInstance = proto.daemonInstance
    operationID = proto.operationID
    requestID = proto.requestID
    ownerClientID = proto.ownerClientID
    kind = proto.kind
    state = proto.state
    cancellable = proto.cancellable
    startedAt = proto.hasStartedAt ? proto.startedAt.date : nil
    finishedAt = proto.hasFinishedAt ? proto.finishedAt.date : nil
    unknownReason = proto.unknownReason
    error = proto.hasError ? ProductError(proto: proto.error) : nil
    settingsResult = DaemonSettingsOperation(proto)
    profileResult = proto.aiProfileResult
    networkResult = DaemonNetworkOperation(proto)
    progress = proto.hasProgress ? DaemonOperationProgress(proto.progress) : nil
    affectedTarget = proto.affectedTarget
    if case .routeSave(let value) = proto.result {
      savedRouteID = value.routeID.isEmpty ? nil : value.routeID
      catalogRevision = value.catalogRevision == 0 ? nil : value.catalogRevision
      savedRouteRevision = value.routeRevision == 0 ? nil : value.routeRevision
      savedConfigRevision = value.configRevision == 0 ? nil : value.configRevision
    } else {
      savedRouteID = nil
      catalogRevision = nil
      savedRouteRevision = nil
      savedConfigRevision = nil
    }
  }
}

extension DaemonConnection {
  /// Reading an operation never resubmits it, including after a daemon restart.
  public func operationState(
    daemonInstance: String, operationID: String
  ) async throws -> DaemonOperationState {
    try await withAuthenticatedClient { client in
      var options = CallOptions.defaults
      options.timeout = .seconds(5)
      let value = try await Pax_V1_PaxService.Client(wrapping: client).getOperation(
        .with {
          $0.daemonInstance = daemonInstance
          $0.operationID = operationID
        }, options: options)
      return DaemonOperationState(proto: value)
    }
  }
}

/// A frozen Profile result; saved metadata and provider directory status stay separate.
public struct DaemonAIProfileOperation: Sendable, Equatable {
  public let workspace: AIProfilesWorkspace?
  public let plan: AIProfilePlan?
  public let profileID: String
  public let deletedProfileID: String
  public let outcome: String
  public let operationID: String

  init(_ value: Pax_V1_AIProfileOperationResult) {
    workspace = value.hasWorkspace ? AIProfilesWorkspace(value.workspace) : nil
    plan = value.hasPlan ? try? AIProfilePlan(value.plan) : nil
    profileID = value.profileID
    deletedProfileID = value.deletedProfileID
    outcome = value.outcome
    operationID = value.operationID
  }
}

extension Pax_V1_OperationState {
  fileprivate var aiProfileResult: DaemonAIProfileOperation? {
    guard case .aiProfile(let value) = result else { return nil }
    return DaemonAIProfileOperation(value)
  }
}
