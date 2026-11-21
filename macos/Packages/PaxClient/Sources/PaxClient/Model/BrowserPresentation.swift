import Foundation
import PaxProto

/// An immutable, bounded subset of product text for a single environment run.
public struct BrowserPresentation: Sendable, Equatable {
  public let locale: String
  public let messages: [String: String]
  public init(locale: String, messages: [String: String]) {
    self.locale = locale
    self.messages = messages
  }
  var proto: Pax_V1_BrowserPresentation {
    .with {
      $0.locale = locale
      $0.messages = messages
    }
  }
}
