import Foundation
import LocalAuthentication
import PaxCore
import Security

enum ClaudeKeychainScanner {
  static func scan() -> ClaudeKeychainReading {
    let authentication = LAContext()
    authentication.interactionNotAllowed = true
    let query: [String: Any] = [
      kSecClass as String: kSecClassGenericPassword,
      kSecReturnAttributes as String: true,
      kSecMatchLimit as String: kSecMatchLimitAll,
      kSecUseAuthenticationContext as String: authentication,
    ]
    var attributes: CFTypeRef?
    let status = SecItemCopyMatching(query as CFDictionary, &attributes)
    let outcome: ClaudeKeychainReading.Outcome
    switch status {
    case errSecSuccess:
      // Read service names only. Account names and credential values are never returned to the store.
      if let items = attributes as? [[String: Any]] {
        let services = items.compactMap { $0[kSecAttrService as String] as? String }
        outcome = ClaudeKeychainReading.inspectServices(services)
      } else {
        outcome = .unavailable
      }
    case errSecItemNotFound:
      outcome = ClaudeKeychainReading.inspectServices([])
    case errSecInteractionNotAllowed, errSecAuthFailed:
      outcome = .restricted
    default:
      outcome = .unavailable
    }
    return ClaudeKeychainReading(outcome: outcome, at: .now)
  }
}
