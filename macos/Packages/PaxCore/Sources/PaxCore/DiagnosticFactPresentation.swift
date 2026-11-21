import Foundation

extension NetworkDiagnosticPresentation {
  private static let legacyFactKinds: [String: String] = {
    var values: [String: String] = [:]
    for (key, text) in PresentationContext.chinese.messages where key.hasPrefix("diagnostic.fact.")
    {
      if case .text(let label) = text {
        values[label] = String(key.dropFirst("diagnostic.fact.".count))
      }
    }
    return values
  }()
  public static func factKind(_ fact: NetworkDiagnosticFact) -> String {
    fact.kind ?? legacyFactKinds[fact.label] ?? ""
  }
  public static func factLabel(_ fact: NetworkDiagnosticFact, context: PresentationContext)
    -> String
  {
    if fact.labelText != nil {
      return ProductTextPresentation.text(fact.labelText, original: fact.label, context: context)
    }
    let key = "diagnostic.fact." + factKind(fact)
    return context.messages[key] == nil ? fact.label : context.text(key)
  }
  public static func factValue(_ fact: NetworkDiagnosticFact, context: PresentationContext)
    -> String
  {
    ProductTextPresentation.text(fact.valueText, original: fact.value, context: context)
  }
  public static func factSource(_ fact: NetworkDiagnosticFact, context: PresentationContext)
    -> String
  {
    ProductTextPresentation.text(fact.sourceText, original: fact.source, context: context)
  }
}
