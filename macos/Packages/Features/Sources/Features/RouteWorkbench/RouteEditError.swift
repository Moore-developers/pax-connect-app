import Foundation
import PaxCore

extension RouteWorkbenchStore {
  struct EditError: LocalizedError {
    let message: String
    private let meaning: ProductText?
    init(_ message: String) {
      self.message = message
      meaning = nil
    }
    init(code: String, params: [String: String] = [:]) {
      meaning = ProductText(code: code, params: params)
      message = PresentationContext.chinese.text(code, params)
    }
    func message(context: PresentationContext) -> String {
      ProductTextPresentation.text(meaning, original: message, context: context)
    }
    var errorDescription: String? { message }
  }
}
