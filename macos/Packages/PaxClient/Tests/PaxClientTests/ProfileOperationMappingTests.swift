import Foundation
import PaxProto
import Testing

@testable import PaxClient

struct ProfileOperationMappingTests {
  @Test func savedResultKeepsDiscoveryFailureSeparate() {
    let value = DaemonOperationState(
      proto: .with {
        $0.state = "succeeded"
        $0.aiProfile = .with {
          $0.profileID = "saved"
          $0.operationID = "op"
          $0.workspace = .with {
            $0.profiles = [
              .with {
                $0.id = "saved"
                $0.hasKey_p = true
                $0.modelsState = "failed"
                $0.modelsError = "models_auth_failed"
              }
            ]
          }
        }
      })
    #expect(value.state == "succeeded")
    #expect(value.profileResult?.profileID == "saved")
    #expect(value.profileResult?.workspace?.profiles.first?.modelsError == "models_auth_failed")
    #expect(value.profileResult?.plan == nil)
    #expect(value.networkResult == nil)
  }

  @Test func planAndRecoveryRemainDistinct() {
    let value = DaemonOperationState(
      proto: .with {
        $0.state = "failed"
        $0.error = .with { $0.code = "ai_recovery_required" }
        $0.aiProfile = .with {
          $0.outcome = "recovery_required"
          $0.plan = .with {
            $0.id = "plan"
            $0.cli = "codex"
            $0.expiresUnix = 1_800_000_000
            $0.targetPath = "/controlled/.codex"
          }
        }
      })
    #expect(value.profileResult?.outcome == "recovery_required")
    #expect(value.profileResult?.plan?.targetPath == "/controlled/.codex")
    #expect(value.error?.code == "ai_recovery_required")
  }
}
