import Foundation
import GRPCCore
import GRPCNIOTransportHTTP2Posix
import PaxProto

extension DaemonConnection {
  private func aiRPC<T: Sendable>(
    _ body:
      @Sendable (Pax_V1_AIProfilesService.Client<HTTP2ClientTransport.Posix>, CallOptions)
      async throws -> T
  ) async throws -> T {
    do {
      return try await withAuthenticatedClient {
        client in
        var options = CallOptions.defaults
        options.timeout = .seconds(30)
        return try await body(Pax_V1_AIProfilesService.Client(wrapping: client), options)
      }
    } catch let error as RPCError {
      let allowed: Set<String> = [
        "ai_profile_invalid", "ai_profile_conflict", "ai_profile_in_use",
        "ai_template_unsupported", "ai_target_changed", "ai_target_unsafe", "ai_plan_expired",
        "ai_store_invalid", "ai_commit_uncertain", "ai_recovery_required",
      ]
      if allowed.contains(error.message) { throw ProductError(code: error.message) }
      throw ClientError.classify(error)
    } catch { throw ClientError.classify(error) }
  }
  public func aiProfiles() async throws -> AIProfilesWorkspace {
    try await aiRPC { service, options in
      AIProfilesWorkspace(try await service.queryAIProfiles(.init(), options: options))
    }
  }
  public func refreshAIProfileModels(_ id: String) async throws -> AIProfilesWorkspace {
    try await aiRPC { service, options in
      AIProfilesWorkspace(
        try await service.refreshAIProfileModels(.with { $0.profileID = id }, options: options))
    }
  }
  public func saveAIProfile(_ p: AIProfile, key: String) async throws -> AIProfilesWorkspace {
    try await aiRPC { service, options in
      AIProfilesWorkspace(
        try await service.mutateAIProfile(
          .with {
            $0.operationID = UUID().uuidString
            $0.save = .with {
              $0.profile = p.proto
              $0.expectedRevision = p.revision
              $0.replaceKey = !key.isEmpty
              $0.apiKey = key
            }
          }, options: options))
    }
  }
  public func deleteAIProfile(_ p: AIProfile) async throws -> AIProfilesWorkspace {
    try await aiRPC { service, options in
      AIProfilesWorkspace(
        try await service.mutateAIProfile(
          .with {
            $0.operationID = UUID().uuidString
            $0.delete = .with {
              $0.profileID = p.id
              $0.expectedRevision = p.revision
            }
          }, options: options))
    }
  }
  public func importAIProfile(
    _ cli: AIClient, inspection: String, name: String,
    initialize: Bool = false, multiplier: Double = 1
  ) async throws -> AIProfilesWorkspace {
    try await aiRPC { service, options in
      AIProfilesWorkspace(
        try await service.mutateAIProfile(
          .with {
            $0.operationID = UUID().uuidString
            if initialize {
              $0.initializeTemplate = .with {
                $0.cli = cli.code
                $0.inspectionID = inspection
              }
            } else {
              $0.importProfile = .with {
                $0.cli = cli.code
                $0.inspectionID = inspection
                $0.name = name
                $0.multiplier = multiplier
              }
            }
          }, options: options))
    }
  }
  public func planAIProfile(
    _ cli: AIClient, profile: AIProfile?, binding: AIBinding,
    restore: Bool = false, initialize: Bool = false, modelID: String = ""
  ) async throws -> AIProfilePlan {
    try await aiRPC { service, options in
      try AIProfilePlan(
        try await service.planAIProfileApply(
          .with {
            $0.cli = cli.code
            $0.profileID = profile?.id ?? ""
            $0.revision = profile?.revision ?? 0
            $0.templateRevision = binding.templateRevision
            $0.inspectionID = binding.inspectionID
            $0.restore = restore
            $0.initialize = initialize
            $0.modelID = modelID
          }, options: options))
    }
  }
  public func applyAIProfile(_ plan: AIProfilePlan) async throws
    -> AIProfilesWorkspace
  {
    try await aiRPC { service, options in
      AIProfilesWorkspace(
        try await service.applyAIProfile(
          .with {
            $0.planID = plan.id
            $0.operationID = plan.id
          }, options: options))
    }
  }
}
