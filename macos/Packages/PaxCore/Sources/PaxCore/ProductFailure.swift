import Foundation
import PaxClient

/// Retains an error's meaning for presentation after a language change.
public enum ProductFailure: Sendable {
  case product(ProductError)
  case client(ClientError)
  case original(String)

  public init(_ error: any Error) {
    if let error = error as? ProductError {
      self = .product(error)
    } else if let error = error as? ClientError {
      self = .client(error)
    } else {
      self = .original(ProductErrorPresentation.display(error))
    }
  }
  public func message(context: PresentationContext) -> String {
    switch self {
    case .product(let error): ProductErrorPresentation.message(error, context: context)
    case .client(let error): ProductErrorPresentation.message(error, context: context)
    case .original(let text): text
    }
  }
}
