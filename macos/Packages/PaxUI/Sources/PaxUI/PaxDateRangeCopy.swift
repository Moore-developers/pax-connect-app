import Foundation

/// Product wording supplied by the feature; calendar, selection and validation stay unchanged.
public struct PaxDateRangeCopy {
  public var apply = "应用"
  public var cancel = "取消"
  public var hint = "点选一天后可直接应用，或再选一天组成区间"
  public var selectPrefix = "选择"
  public var spanHint: (Int) -> String = { "（含首尾最多\($0)天）" }
  public var presetTitles: [PaxDatePreset: String] = [:]
  public var navigation = [-12: "上一年", 12: "下一年", -1: "上个月", 1: "下个月"]
  public var weekdays = ["日", "一", "二", "三", "四", "五", "六"]
  public var monthTitle: (Int, Int) -> String = { "\($0)年 \($1)月" }
  public init() {}
}
