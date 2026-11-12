import Foundation

/// 概览「模型用量趋势」的值输入：费用与 Token 两套口径共用同一版式，卡片内用页签切换。缺测写「—」，不补 0。
public struct PaxModelTrendModel: Equatable {
  /// 左栏「标签 … 数值」行（今日请求、Token 或费用、CLI 占比）。
  public struct Fact: Equatable {
    public var label: String
    public var value: String
    public init(_ label: String, _ value: String) {
      self.label = label
      self.value = value
    }
  }

  /// 一个口径的今日大数字、昨日 / 日均、附加行与近 7 日按天柱。
  public struct Series: Equatable {
    public var value: String
    public var yesterday: String
    public var average: String
    public var facts: [Fact]
    public var days: [PaxChartSample]
    public init(
      value: String = "—",
      yesterday: String = "—", average: String = "—", facts: [Fact] = [],
      days: [PaxChartSample] = []
    ) {
      self.value = value
      self.yesterday = yesterday
      self.average = average
      self.facts = facts
      self.days = days
    }
  }

  public var updated: String
  public var cost: Series
  public var tokens: Series
  public var currentIndex: Int
  public var groups: [PaxUsageFilterGroup]
  /// 选择由页面持有，组件刷新时按值恢复。
  public var selection: PaxUsageFilter
  /// 全量有数据但所选 Profile / 模型没有请求，保留筛选下拉与口径页签。
  public var selectionEmpty: String?
  /// 无采集或无样本时的整卡空态标题。
  public var empty: String?
  public init(
    updated: String = "", cost: Series = Series(), tokens: Series = Series(), currentIndex: Int = 6,
    empty: String? = nil, groups: [PaxUsageFilterGroup] = [], selection: PaxUsageFilter = .all,
    selectionEmpty: String? = nil
  ) {
    self.updated = updated
    self.cost = cost
    self.tokens = tokens
    self.currentIndex = currentIndex
    self.empty = empty
    self.groups = groups
    self.selection = selection
    self.selectionEmpty = selectionEmpty
  }
}
