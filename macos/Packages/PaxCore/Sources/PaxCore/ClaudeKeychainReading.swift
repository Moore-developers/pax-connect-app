import Foundation

/// A local observation of Keychain item metadata, with no login or token data.
public struct ClaudeKeychainReading: Sendable, Equatable {
  public enum Outcome: Sendable, Equatable {
    case checked(doctorProbeCount: Int, defaultCount: Int, otherCount: Int)
    case restricted
    case unavailable
  }

  public let outcome: Outcome
  public let at: Date

  public init(outcome: Outcome, at: Date) {
    self.outcome = outcome
    self.at = at
  }

  public static func inspectServices(_ services: [String]) -> Outcome {
    let standard = "Claude Code-credentials"
    let suffixPrefix = standard + "-"
    var doctorProbeCount = 0
    var defaultCount = 0
    var otherCount = 0
    for service in services {
      if service == "Claude Code-doctor-probe" {
        doctorProbeCount += 1
      } else if service == standard {
        defaultCount += 1
      } else if service.hasPrefix(suffixPrefix) {
        let suffix = service.dropFirst(suffixPrefix.count)
        if suffix.utf8.count == 8,
          suffix.utf8.allSatisfy({
            (48...57).contains($0) || (65...70).contains($0) || (97...102).contains($0)
          })
        {
          otherCount += 1
        }
      }
    }
    return .checked(
      doctorProbeCount: doctorProbeCount, defaultCount: defaultCount, otherCount: otherCount)
  }
}
