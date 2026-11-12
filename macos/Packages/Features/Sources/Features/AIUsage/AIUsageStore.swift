import Foundation
import Observation
import PaxCore
import PaxUI

@MainActor @Observable
final class AIUsageStore {
  enum Page: String, CaseIterable {
    case overview = "概览"
    case sessions = "会话分析"
    case profiles = "Profile"
    case logs = "日志"
  }
  enum Intent {
    case page(Page)
    case period(AIUsagePeriod)
    case dates(Date, Date)
    case measureCost(Bool)
    case trendFilter(PaxUsageFilter)
    case profileMeasureCost(Bool)
    case profile(AIProfile)
    case remove(String)
    case bind(AIClient, String)
    case logCostRange(Int64?, Int64?)
    case logClient(AIClient?)
    case logProfile(String?)
    case logModel(String?)
    case logResult(Bool?)
    case resetLogFilters
    case openLogs(model: String, profile: String?)
  }
  var configurableClients: [AIClient] { [.claude, .codex] }
  let preview: Bool
  let previewModels: [AIModel]
  let previewRequests: [AIRequest]
  let data: AIUsageDataStore?
  let sessionOptions = AISessionOptions()
  var logPage = 0
  var requests: [AIRequest] { preview ? previewRequests : data?.logs?.requests ?? [] }
  var models: [AIModel] {
    if preview { return previewModels }
    let rows = (data?.buckets ?? []) + (data?.lifetime ?? [])
    let ids = Set(rows.map(\.modelID)).filter { !AIUsageFormatting.isSyntheticModel($0) }
    return ids.sorted().map { id in
      AIModel(
        id: id, name: id, clients: Set(rows.filter { $0.modelID == id }.map(\.client)),
        inputPrice: nil, outputPrice: nil)
    }
  }
  let configuration: AIProfilesStore
  var profiles: [AIProfile] { configuration.workspace.profiles.filter { $0.nativeCLI != .harness } }
  var bindings: [AIClient: AIBinding] { configuration.workspace.bindings }
  private(set) var page: Page = .overview
  private(set) var period: AIUsagePeriod = .today
  private(set) var start: Date
  private(set) var end: Date
  private(set) var measureCost = true
  private(set) var trendFilter = PaxUsageFilter.all
  private(set) var profileMeasureCost = true
  private(set) var minimumCost: Int64?
  private(set) var maximumCost: Int64?
  private(set) var logClient: AIClient?
  private(set) var logProfileID: String?
  private(set) var logModelID: String?
  private(set) var logResult: Bool?
  private(set) var error: String?
  let initialNow: Date
  var now: Date { preview ? initialNow : Date() }
  let calendar: Calendar
  init(
    preview: Bool = false, now: Date = Date(), calendar: Calendar = .current,
    configuration: AIProfilesStore? = nil, data: AIUsageDataStore? = nil
  ) {
    self.preview = preview
    self.initialNow = now
    self.data = data
    self.calendar = calendar
    start = now
    end = now
    previewModels = preview ? AIUsageFixture.models : []
    var workspace = AIProfilesWorkspace()
    if preview {
      workspace.profiles = AIUsageFixture.profiles
      workspace.bindings = AIUsageFixture.bindings
    }
    self.configuration = configuration ?? AIProfilesStore(preview: preview ? workspace : nil)
    previewRequests = preview ? AIUsageFixture.requests(now: now, calendar: calendar) : []
  }
  var interval: Range<Date> {
    period.interval(now: now, start: start, end: end, calendar: calendar)
  }
  var previousInterval: Range<Date> {
    period.previousInterval(now: now, start: start, end: end, calendar: calendar)
  }
  var selected: [AIRequest] { requests.filter { interval.contains($0.at) } }
  var summary: AIUsageSummary { usageSummary() }
  var hasLogFilters: Bool {
    minimumCost != nil || maximumCost != nil || logClient != nil || logProfileID != nil
      || logModelID != nil
      || logResult != nil
  }
  var logProfileOptions: [(String, String)] {
    var names = Dictionary(uniqueKeysWithValues: profiles.map { ($0.id, $0.name) })
    for b in (data?.lifetime ?? []) where b.client != .harness {
      let id = AIUsageTrendFilter.profileKey(profileID: b.profileID, client: b.client)
      if names[id] == nil { names[id] = b.profileName }
    }
    for request in requests where request.client != .harness {
      let id = AIUsageTrendFilter.profileKey(profileID: request.profileID, client: request.client)
      if names[id] == nil { names[id] = request.profileName }
    }
    return names.sorted { $0.value == $1.value ? $0.key < $1.key : $0.value < $1.value }
      .map { ($0.key, $0.value) }
  }
  var filteredLogs: [AIRequest] { preview ? logs(applyingResult: true) : requests }
  var logSummary: AIUsageSummary {
    preview
      ? AIUsageSummary(requests: filteredLogs)
      : AIUsageSummary(totals: data?.logs?.totals ?? AIUsageTotals())
  }
  /// Counts for 全部 / 成功 / 失败 under the other filters, so the segments never depend on themselves.
  var logCounts: (all: Int, success: Int, failed: Int) {
    if !preview { return (data?.logs?.all ?? 0, data?.logs?.success ?? 0, data?.logs?.failed ?? 0) }
    let rows = logs(applyingResult: false)
    let success = rows.filter(\.succeeded).count
    return (rows.count, success, rows.count - success)
  }
  private func logs(applyingResult: Bool) -> [AIRequest] {
    selected.filter { request in
      (logClient == nil || request.client == logClient)
        && (logProfileID == nil || request.profileID == logProfileID)
        && (logModelID == nil || request.modelID == logModelID)
        && (!applyingResult || logResult == nil || request.succeeded == logResult)
        && (minimumCost.map { Int64((request.originalCost * 1_000_000).rounded()) >= $0 } ?? true)
        && (maximumCost.map { Int64((request.originalCost * 1_000_000).rounded()) <= $0 } ?? true)
    }
  }
  var ranking: [(AIModel, AIUsageSummary)] {
    let groups: [(AIModel, AIUsageSummary)] = models.compactMap { model in
      let sum = usageSummary(model: model.id)
      guard sum.count > 0, sum.tokens > 0 else { return nil }
      return (model, sum)
    }
    let ordered = groups.sorted { left, right in
      if left.1.originalCost == right.1.originalCost {
        if left.1.tokens != right.1.tokens { return left.1.tokens > right.1.tokens }
        return left.0.id < right.0.id
      }
      return left.1.originalCost > right.1.originalCost
    }
    return Array(ordered.prefix(10))
  }
  var buckets: [(Date, AIUsageSummary)] { trendBuckets() }
  /// A Profile that any CLI is bound to cannot be deleted.
  func isBound(_ profileID: String) -> Bool {
    bindings.values.contains { $0.profileID == profileID }
  }
  func model(_ id: String) -> String { models.first { $0.id == id }?.name ?? id }
  func profile(_ id: String) -> String { profiles.first { $0.id == id }?.name ?? id }
  func perform(_ intent: Intent) {
    error = nil
    defer { syncUsage() }
    switch intent {
    case .logCostRange, .logClient, .logProfile, .logModel, .logResult, .resetLogFilters, .openLogs:
      logPage = 0
    default: break
    }
    switch intent {
    case .page(let value): page = value
    case .period(let value):
      period = value
      logPage = 0
    case .dates(let from, let to):
      guard from <= to else {
        error = "结束日期不能早于开始日期"
        return
      }
      guard
        (calendar.dateComponents(
          [.day], from: calendar.startOfDay(for: from), to: calendar.startOfDay(for: to)
        ).day ?? 32) < 31
      else {
        error = "自定义范围最多31天（含首尾）"
        return
      }
      logPage = 0
      start = from
      end = to
      period = .custom
    case .measureCost(let value): measureCost = value
    case .trendFilter(let value): trendFilter = value
    case .profileMeasureCost(let value): profileMeasureCost = value
    case .logCostRange(let minimum, let maximum):
      minimumCost = minimum
      maximumCost = maximum
    case .logClient(let value): logClient = value
    case .logProfile(let value): logProfileID = value
    case .logModel(let value): logModelID = value
    case .logResult(let value): logResult = value
    case .resetLogFilters:
      minimumCost = nil
      maximumCost = nil
      logClient = nil
      logProfileID = nil
      logModelID = nil
      logResult = nil
    case .openLogs(let model, let profile):
      minimumCost = nil
      maximumCost = nil
      logClient = nil
      logResult = nil
      logModelID = model
      logProfileID = profile
      page = .logs
    case .profile(let value):
      guard preview else {
        error = "请在编辑浮层保存配置"
        return
      }
      configuration.savePreview(value)
      error = configuration.error
    case .remove(let id):
      guard let p = profiles.first(where: { $0.id == id }) else { return }
      guard !isBound(id) else {
        error = "此 Profile 仍在使用，请先切换"
        return
      }
      if preview {
        configuration.deletePreview(p)
        error = configuration.error
      } else {
        Task { await configuration.delete(p) }
      }
    case .bind(let client, let id):
      guard preview, let p = profiles.first(where: { $0.id == id }) else { return }
      configuration.previewBind(client, profile: p)

    }
  }
}
