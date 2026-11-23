import PaxProto

public struct AntigravityModels: Sendable {
  public struct Model: Sendable {
    public let id: String
    public let name: String
    public let plans: [String]
    public let kind: String
    public let retiredOn: String

    init(_ value: Pax_V1_AntigravityModel) {
      id = value.id
      name = value.name
      plans = value.plans
      kind = value.kind
      retiredOn = value.retiredOn
    }
  }

  public struct Rate: Sendable {
    public let modelID: String
    public let input: Double
    public let cacheRead: Double
    public let cacheWrite: Double?
    public let output: Double
    public let effectiveOn: String

    init(_ value: Pax_V1_AntigravityRate) {
      modelID = value.modelID
      input = value.input
      cacheRead = value.cacheRead
      cacheWrite = value.hasCacheWrite ? value.cacheWrite : nil
      output = value.output
      effectiveOn = value.effectiveOn
    }
  }

  public let models: [Model]
  public let rates: [Rate]

  init(_ value: Pax_V1_AntigravityModels) {
    models = value.models.map(Model.init)
    rates = value.rates.map(Rate.init)
  }
}
