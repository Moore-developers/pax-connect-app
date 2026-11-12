import PaxCore
import PaxUI

/// 趋势卡「Profile + 模型」筛选的共用规则：分组键、选项分组与匹配。
enum AIUsageTrendFilter {
  static let harnessKey = "__dsh__"
  static let unassignedKey = "__unassigned__"

  /// 空 profileID 时：DeepSeek 官方（harness）与未配置各自一组。
  static func profileKey(
    profileID: String, client: AIClient, serviceSource: AIServiceSource = .unrecorded
  ) -> String {
    if !profileID.isEmpty { return profileID }
    if client == .harness { return harnessKey }
    if serviceSource == .unknown || serviceSource == .unrecorded { return unassignedKey }
    return "__source:" + serviceSource.rawValue + "__"
  }

  static func fallbackName(_ key: String) -> String? {
    if key == harnessKey { return "DeepSeek 官方" }
    if key == unassignedKey { return "来源待确认" }
    if key.hasPrefix("__source:"), key.hasSuffix("__"),
      let source = AIServiceSource(rawValue: String(key.dropFirst(9).dropLast(2)))
    {
      return source.label
    }
    return nil
  }

  /// 一行有请求的用量，与来源（桶或请求）无关。
  struct Row {
    var profileID: String
    var client: AIClient
    var profileName: String
    var modelID: String
    var count: Int
    var cost: Double
    var tokens: Int
    var serviceSource: AIServiceSource = .unrecorded
  }

  static func matches(
    _ filter: PaxUsageFilter, profileID: String, client: AIClient, modelID: String,
    serviceSource: AIServiceSource = .unrecorded
  ) -> Bool {
    guard let wanted = filter.profileID else { return true }
    return profileKey(profileID: profileID, client: client, serviceSource: serviceSource) == wanted
      && (filter.modelID == nil || filter.modelID == modelID)
  }

  /// Profile 与组内模型都按费用降序、Token 降序、名称 / id 升序；模型行不含合成模型，Profile 只要有请求就出现。
  /// 所选项已无请求时保留，挂在其 Profile 组下，Profile 也没有就补出该组。
  static func groups(
    rows: [Row], selection: PaxUsageFilter,
    profileName: (String) -> String?, modelName: (String) -> String = { $0 }
  ) -> [PaxUsageFilterGroup] {
    struct Sum {
      var cost = 0.0
      var tokens = 0
    }
    var profiles: [String: (name: String, sum: Sum, models: [String: Sum])] = [:]
    for row in rows where row.count > 0 {
      let key = profileKey(
        profileID: row.profileID, client: row.client, serviceSource: row.serviceSource)
      let name =
        fallbackName(key) ?? profileName(key) ?? (row.profileName.isEmpty ? key : row.profileName)
      var entry = profiles[key] ?? (name, Sum(), [:])
      entry.sum.cost += row.cost
      entry.sum.tokens += row.tokens
      if !AIUsageFormatting.isSyntheticModel(row.modelID) {
        entry.models[row.modelID, default: Sum()].cost += row.cost
        entry.models[row.modelID, default: Sum()].tokens += row.tokens
      }
      profiles[key] = entry
    }
    func ordered<T>(_ items: [(id: String, name: String, sum: Sum, value: T)]) -> [T] {
      items.sorted {
        if $0.sum.cost != $1.sum.cost { return $0.sum.cost > $1.sum.cost }
        if $0.sum.tokens != $1.sum.tokens { return $0.sum.tokens > $1.sum.tokens }
        return ($0.name, $0.id) < ($1.name, $1.id)
      }.map(\.value)
    }
    var groups = ordered(
      profiles.map { key, entry in
        let models = ordered(
          entry.models.map { id, sum in
            (id, modelName(id), sum, PaxUsageFilterGroup.Model(id: id, name: modelName(id)))
          })
        return (
          key, entry.name, entry.sum, PaxUsageFilterGroup(id: key, name: entry.name, models: models)
        )
      })
    if let wanted = selection.profileID {
      if !groups.contains(where: { $0.id == wanted }) {
        groups.append(
          .init(id: wanted, name: fallbackName(wanted) ?? profileName(wanted) ?? wanted))
      }
      if let modelID = selection.modelID, let index = groups.firstIndex(where: { $0.id == wanted }),
        !groups[index].models.contains(where: { $0.id == modelID })
      {
        groups[index].models.append(.init(id: modelID, name: modelName(modelID)))
      }
    }
    return groups
  }
}
