import Foundation

public struct PaxTimeSeriesCopy {
  public var missing = "未采集"
  public var partial = "部分采集"
  public var total = "合计 "
  public var range = "范围 "
  public var noData = "暂无数据"
  public var latest = "最新 "
  public var maximum = "最大 "
  public var samples: (Int) -> String = { "最近 \($0) 个有效样本" }
  public var gaps: (Int) -> String = { "未采集 \($0) 个时段" }
  public var coverage: (String) -> String = { "自 \($0) 起有记录" }
  public init() {}
}
