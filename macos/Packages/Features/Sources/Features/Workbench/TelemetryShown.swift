import PaxCore

extension TelemetryStore {
  /// 页面是否在显示。`focus: false` 不看 App 前台：PaxRefresh 驱动的读取以其 gate 为唯一可见性权威，
  /// 后台启动 / 失焦但窗口可见时不能因 appActive 丢弃已放行的读取。`focus: true` 保持原行为（演示 / 回退路径）。
  func isShown(_ page: TelemetryPage, focus: Bool, tab: Bool = true) -> Bool {
    guard visiblePage == page, windowVisible else { return false }
    if focus && !appActive { return false }
    return !tab || page != .traffic || trafficTabActive
  }
}

/// This projection is display-only: its revoked token can never be used for another read.
extension TrafficPage {
  var withoutLogEvidence: TrafficPage {
    var page = self
    func strip(_ row: TrafficRow) -> TrafficRow {
      var row = row
      row.recentResponses = []
      row.recentResponsesState = "loading"
      row.recentResponsesError = ""
      return row
    }
    page.items = items.map(strip)
    page.unknownSource = unknownSource.map(strip)
    page.dnsSummary = dnsSummary.map(strip)
    page.readToken = ""
    page.nextCursor = ""
    return page
  }
}
