import Foundation
import PaxProto

/// Additive usage counters. Cost values are USD; every request is priced.
public struct AIUsageTotals: Equatable, Sendable {
  public var count = 0
  public var success = 0
  public var input = 0
  public var read = 0
  public var write = 0
  public var output = 0
  public var costs = [Double](repeating: 0, count: 4)
  public var billedCosts = [Double](repeating: 0, count: 4)
  public var cost = 0.0
  public var tpsSum = 0.0
  public var elapsedSum = 0.0
  public var tpsCount = 0
  public var elapsedCount = 0
  public init() {}
  init(_ p: Pax_V1_AIUsageTotals) {
    count = Int(p.count)
    success = Int(p.success)
    input = Int(p.input)
    read = Int(p.read)
    write = Int(p.write)
    output = Int(p.output)
    costs = (0..<4).map { $0 < p.costsMicro.count ? Double(p.costsMicro[$0]) / 1_000_000 : 0 }
    billedCosts = (0..<4).map {
      $0 < p.billedCostsMicro.count ? Double(p.billedCostsMicro[$0]) / 1_000_000 : 0
    }
    cost = Double(p.costMicro) / 1_000_000
    tpsSum = p.tpsSum
    elapsedSum = p.elapsedSum
    tpsCount = Int(p.tpsCount)
    elapsedCount = Int(p.elapsedCount)
  }
  public mutating func add(_ v: Self) {
    count += v.count
    success += v.success
    input += v.input
    read += v.read
    write += v.write
    output += v.output
    for i in 0..<4 {
      costs[i] += v.costs[i]
      billedCosts[i] += v.billedCosts[i]
    }
    cost += v.cost
    tpsSum += v.tpsSum
    elapsedSum += v.elapsedSum
    tpsCount += v.tpsCount
    elapsedCount += v.elapsedCount
  }
}
public struct AIUsageBucket: Equatable, Sendable {
  public let at: Date
  public let client: AIClient
  public let profileID: String
  public let profileName: String
  public let modelID: String
  public let totals: AIUsageTotals
  public init(
    at: Date, client: AIClient, profileID: String = "", profileName: String = "", modelID: String,
    totals: AIUsageTotals
  ) {
    self.at = at
    self.client = client
    self.profileID = profileID
    self.profileName = profileName
    self.modelID = modelID
    self.totals = totals
  }
  init?(_ p: Pax_V1_AIUsageBucket) {
    guard let cli = AIClient(code: p.cli) else { return nil }
    at = Date(timeIntervalSince1970: Double(p.atMs) / 1000)
    client = cli
    profileID = p.profileID
    profileName = p.profileName
    modelID = p.modelID
    totals = AIUsageTotals(p.totals)
  }
}
public struct AIUsageRequest: Identifiable, Equatable, Sendable {
  public let id: String
  public let at: Date
  public let profileID: String
  public let profileName: String
  public let modelID: String
  public let client: AIClient
  public let succeeded: Bool
  public let input: Int
  public let read: Int
  public let write: Int
  public let output: Int
  public let inputPrice: Double?
  public let outputPrice: Double?
  public let multiplier: Double
  public let tps: Double?
  public let elapsed: Double?
  public var streaming = true
  public var errorMessage: String?
  public var componentPrices: [Double?] = []
  public var priceVersion = ""
  public var reasoningEffort = ""
  public var effortSource = ""
  public var cacheWriteShortPrice: Double?
  public var cacheWriteLongPrice: Double?
  public var cacheWriteShort: Int?
  public var cacheWriteLong: Int?
  private var recordedCost: Double?
  private var recordedAmounts: [Double]?
  public var ordinary: Int { max(0, input - read - write) }
  public var tokens: Int { input + output }
  public var amounts: [Double] {
    if let recordedAmounts { return recordedAmounts }
    guard let inputPrice, let outputPrice else { return [0, 0, 0, 0] }
    return [
      Double(ordinary) * inputPrice, Double(read) * inputPrice * 0.1,
      Double(write) * inputPrice * 1.25, Double(output) * outputPrice,
    ].map { $0 / 1_000_000 }
  }
  public var originalCost: Double { amounts.reduce(0, +) }
  public var cost: Double { recordedCost ?? originalCost * multiplier }
  public init(
    id: String, at: Date, profileID: String, profileName: String, modelID: String,
    client: AIClient, succeeded: Bool, input: Int, read: Int, write: Int, output: Int,
    inputPrice: Double?, outputPrice: Double?, multiplier: Double, tps: Double?,
    elapsed: Double?, streaming: Bool = true, errorMessage: String? = nil
  ) {
    self.id = id
    self.at = at
    self.profileID = profileID
    self.profileName = profileName
    self.modelID = modelID
    self.client = client
    self.succeeded = succeeded
    self.input = input
    self.read = read
    self.write = write
    self.output = output
    self.inputPrice = inputPrice
    self.outputPrice = outputPrice
    self.multiplier = multiplier
    self.tps = tps
    self.elapsed = elapsed
    self.streaming = streaming
    self.errorMessage = errorMessage
  }
  init?(_ p: Pax_V1_AIUsageRequest) {
    guard let cli = AIClient(code: p.cli) else { return nil }
    self.init(
      id: p.id, at: Date(timeIntervalSince1970: Double(p.atMs) / 1000),
      profileID: p.profileID, profileName: p.profileName, modelID: p.modelID, client: cli,
      succeeded: p.succeeded, input: Int(p.input), read: Int(p.read), write: Int(p.write),
      output: Int(p.output), inputPrice: p.hasInputPrice ? p.inputPrice : nil,
      outputPrice: p.hasOutputPrice ? p.outputPrice : nil, multiplier: p.multiplier,
      tps: p.hasTps ? p.tps : nil,
      elapsed: p.hasElapsed ? p.elapsed : nil, streaming: p.streaming,
      errorMessage: p.errorCode.isEmpty ? nil : p.errorCode)
    componentPrices = p.componentPrices.map { $0.hasValue ? $0.value : nil }
    priceVersion = p.priceVersion
    reasoningEffort = p.reasoningEffort
    effortSource = p.effortSource
    cacheWriteShortPrice = p.hasCacheWriteShortPrice ? p.cacheWriteShortPrice : nil
    cacheWriteLongPrice = p.hasCacheWriteLongPrice ? p.cacheWriteLongPrice : nil
    cacheWriteShort = p.hasCacheWriteShort ? Int(p.cacheWriteShort) : nil
    cacheWriteLong = p.hasCacheWriteLong ? Int(p.cacheWriteLong) : nil
    recordedCost = Double(p.costMicro) / 1_000_000
    recordedAmounts = (0..<4).map {
      $0 < p.costsMicro.count ? Double(p.costsMicro[$0]) / 1_000_000 : 0
    }
  }
}
public struct AIUsageCollectionState: Equatable, Sendable {
  public var catalogRevision: UInt64 = 0
  public var catalogReady = false
  public var catalogError = ""
  public var revision: UInt64 = 0
  /// Advances on delete, rebuild and reprice; a changed value invalidates cached detail.
  public var generation: UInt64 = 0
  public var backfilling = false
  public var skipped = 0
  public var errorCode = ""
  /// The database is from an older schema; only the rebuild program can replace it.
  public var rebuildRequired: Bool { errorCode == "ai_usage_rebuild_required" }
  public init() {}
  init(_ p: Pax_V1_AIUsageState) {
    revision = p.revision
    generation = p.generation
    backfilling = p.backfilling
    skipped = Int(p.skipped)
    errorCode = p.errorCode
    catalogRevision = p.catalogRevision
    catalogReady = p.catalogReady
    catalogError = p.catalogError
  }
}
public struct AIUsageBucketPage: Sendable {
  public let buckets: [AIUsageBucket]
  public let state: AIUsageCollectionState
  public let notModified: Bool
  /// Latest request per client, independent of the queried range.
  public let lastUsed: [AIClient: Date]
  public init(
    buckets: [AIUsageBucket], state: AIUsageCollectionState, notModified: Bool,
    lastUsed: [AIClient: Date] = [:]
  ) {
    self.buckets = buckets
    self.state = state
    self.notModified = notModified
    self.lastUsed = lastUsed
  }
  init(_ p: Pax_V1_AIUsageBuckets) {
    buckets = p.buckets.compactMap(AIUsageBucket.init)
    state = AIUsageCollectionState(p.state)
    notModified = p.notModified
    lastUsed = Dictionary(
      p.lastUsed.compactMap { item in
        AIClient(code: item.cli).map { ($0, Date(timeIntervalSince1970: Double(item.atMs) / 1000)) }
      }, uniquingKeysWith: max)
  }
}
public struct AIUsageLogPage: Sendable {
  public let requests: [AIUsageRequest]
  public let all: Int
  public let success: Int
  public let failed: Int
  public let totals: AIUsageTotals
  public let state: AIUsageCollectionState?
  public init(
    requests: [AIUsageRequest], all: Int, success: Int, failed: Int, totals: AIUsageTotals,
    state: AIUsageCollectionState? = nil
  ) {
    self.requests = requests
    self.state = state
    self.all = all
    self.success = success
    self.failed = failed
    self.totals = totals
  }
  init(_ p: Pax_V1_AIUsageLogs) {
    requests = p.requests.compactMap(AIUsageRequest.init)
    state = p.hasState ? AIUsageCollectionState(p.state) : nil
    all = Int(p.all)
    success = Int(p.success)
    failed = Int(p.failed)
    totals = AIUsageTotals(p.totals)
  }
}
public struct AIUsageFilter: Equatable, Sendable {
  public var minOfficialCostMicro: Int64?
  public var maxOfficialCostMicro: Int64?
  public var result: Bool?
  public var query = ""
  public var profile = ""
  public var model = ""
  public var cli = ""
  public var page = 0
  public init() {}
}

/// Which stored requests a repricing covers: inclusive local days, or everything.
public enum AIRepriceRange: Equatable, Sendable {
  case days(from: String, to: String)  // "YYYY-MM-DD"
  case all
}
/// What a repricing did, or for a dry run would do. Costs are USD (multiplied).
public struct AIRepriceResult: Equatable, Sendable {
  public let requests: Int
  /// Requests that only survive in hourly buckets and are rescaled per bucket.
  public let aggregatedRequests: Int
  public let oldCost: Double
  public let newCost: Double
  public let dryRun: Bool
  public init(
    requests: Int, aggregatedRequests: Int = 0, oldCost: Double, newCost: Double, dryRun: Bool
  ) {
    self.requests = requests
    self.aggregatedRequests = aggregatedRequests
    self.oldCost = oldCost
    self.newCost = newCost
    self.dryRun = dryRun
  }
  init(_ p: Pax_V1_RepriceAIUsageResult) {
    requests = Int(p.requests)
    aggregatedRequests = Int(p.aggregatedRequests)
    oldCost = Double(p.oldCostMicro) / 1_000_000
    newCost = Double(p.newCostMicro) / 1_000_000
    dryRun = p.dryRun
  }
}
