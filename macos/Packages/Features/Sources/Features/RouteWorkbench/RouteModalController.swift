import AppKit
import PaxCore

/// 保留线路编辑的错误映射，尺寸/控件使用工作台共享浮层。
@MainActor
class RouteModalController: WorkbenchModalController {
  override func showError(_ error: Error) {
    super.showError(error)
    pane.status.stringValue = ProductErrorPresentation.display(error)
  }
}
