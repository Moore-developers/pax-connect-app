import AppKit
import PaxCore
import PaxUI

/// 概览「流量分析」的接线：列表与流量页共用 `RoutingTrafficList`，窗口固定今日，最多十行。
extension WorkbenchDashboardViewController {
  static func makeAnalysisCard() -> PaxTrafficAnalysisCard {
    PaxTrafficAnalysisCard(
      periodTitle: RoutingWorkbenchStore.Period.day.title,
      dimensions: RoutingWorkbenchStore.Dimension.allCases.map(\.title))
  }
  func installAnalysis() {
    analysisList.onOpen = { [weak self] id in self?.showRequest(id) }
    analysis.onDimension = { [weak self] index in
      if let value = RoutingWorkbenchStore.Dimension(rawValue: index) {
        self?.detailTask?.cancel()
        self?.changeAnalysis(.dimension(value))
      }
    }
    analysis.onFilter = { [weak self] index in
      if let value = RoutingWorkbenchStore.Direction(rawValue: index) {
        self?.changeAnalysis(.direction(value))
      }
    }
  }
  /// 真实数据由概览自己的刷新循环重读；演示数据随 Store 变化即时重绘。
  private func changeAnalysis(_ intent: RoutingWorkbenchStore.Intent) {
    analysisStore.perform(intent)
    if telemetry != nil {
      analysisStore.stopLive()
      restartTelemetry()
    }
  }
  func renderAnalysis() {
    analysisList.render(
      in: analysis, enabled: liveRoutes == nil || telemetry?.trafficAvailable == true)
  }
  public func focusSearch() {
    tooltip.close()
    let sheet = WorkbenchSheet(title: "搜索流量分析", explanation: "搜索今日的目标、线路或规则。")
    let input = NSSearchField()
    input.stringValue = analysisStore.search
    sheet.addView(input)
    sheet.addAction("应用筛选") { [weak self, weak sheet] in
      self?.changeAnalysis(.search(input.stringValue))
      sheet?.dismissSheet()
    }
    presentAsSheet(sheet)
  }
  func showRequest(_ id: String) {
    if telemetry != nil {
      detailTask?.cancel()
      detailTask = analysisList.loadDetail(
        id, visibleOn: .dashboard,
        present: { [weak self] in self?.presentWorkbenchModal($0) },
        fail: { [weak self] in
          self?.notice.isHidden = false
          self?.notice.stringValue = $0
        })
      return
    }
    guard presentedViewControllers?.isEmpty != false,
      let row = analysisStore.visibleRows.first(where: { $0.id == id })
    else { return }
    tooltip.close()
    presentWorkbenchModal(
      WorkbenchTrafficDetailController(
        snapshot: analysisStore.trafficDetail(for: row), tooltip: tooltip,
        icons: analysisList.icons))
  }
}

extension RoutingTrafficList {
  /// 把当前数据填入概览卡片：最多十行，不足五行用占位行补齐固定高度。
  func render(in card: PaxTrafficAnalysisCard, enabled: Bool = true) {
    let count = min(
      PaxTheme.Dashboard.maximumRows,
      store.telemetry == nil ? store.visibleRows.count : store.livePage.items.count)
    let message =
      store.liveLoading && count == 0 ? "正在读取流量" : emptyMessage(unknownSourceBelow: false)
    card.show(activeTable)
    let changed = render(
      emptyMessage: "", limit: PaxTheme.Dashboard.maximumRows,
      minimumRows: PaxTheme.Dashboard.minimumRows)
    card.apply(
      dimensionIndex: store.dimension.rawValue, filterIndex: min(2, store.direction.rawValue),
      rowCount: count, emptyMessage: count == 0 ? message : nil, controlsEnabled: enabled)
    if changed { activeTable.preparePageRows() }
  }
}
