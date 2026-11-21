import Foundation

/// One retained read result: product wording is resolved only when the page presents it.
public struct RetainedProductMessage: Sendable {
  public let original: String
  let key: String?
  private let failure: ProductFailure?
  private let parameters: [String: String]
  public init(original: String) {
    self.original = original
    parameters = [:]
    key = nil
    failure = nil
  }
  public init(
    key: String? = nil, parameters: [String: String] = [:], failure: ProductFailure? = nil
  ) {
    self.parameters = parameters
    self.key = key
    self.failure = failure
    if let key {
      var values = parameters
      if let failure { values["error"] = failure.message(context: .chinese) }
      original = PresentationContext.chinese.text(key, values)
    } else {
      original = failure?.message(context: .chinese) ?? ""
    }
  }
  public func text(context: PresentationContext) -> String {
    if let key {
      var values = parameters
      if let failure { values["error"] = failure.message(context: context) }
      return context.text(key, values)
    }
    return failure?.message(context: context) ?? original
  }
}
