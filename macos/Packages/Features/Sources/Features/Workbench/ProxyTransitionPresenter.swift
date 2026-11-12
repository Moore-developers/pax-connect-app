import AppKit
import PaxCore

/// The single UI seam for proxy transitions. Dashboard and Routes only provide
/// their presenter and the shared ConnectionStore; authorization, certificate
/// consent, and the final intent stay identical in both places.
@MainActor
public enum ProxyTransitionPresenter {
  public static func request(
    from presenter: NSViewController,
    connection: ConnectionStore,
    enabled: Bool
  ) {
    guard connection.canRequestProxy(enabled) else { return }
    if enabled && connection.needsServiceAuthorization {
      Task { await connection.setProxyFromWorkbench(true) }
      return
    }
    if enabled, let route = connection.proxyConfirmationRoute {
      presenter.presentAsSheet(
        RouteTLSSettingsViewController(store: connection, route: route, enableAfterSave: true))
      return
    }
    Task { await connection.setProxyFromWorkbench(enabled) }
  }
}

extension ProxyTransitionPresenter {
  static func presentConflict(
    from presenter: NSViewController, connection: ConnectionStore, error: ProductError
  ) {
    guard let window = presenter.view.window, presenter.presentedViewControllers?.isEmpty != false
    else { return }
    let alert = NSAlert()
    alert.messageText = error.code == "pax_tun_stale" ? "检测到 Pax 的旧网络接管" : "其它 TUN 正在接管流量"
    alert.informativeText =
      error.code == "pax_tun_stale"
      ? "请先关闭旧 Pax 实例。只处理本实例可确认的网络资源；不会删除其它工具的路由。"
      : ProductErrorPresentation.message(error)
    alert.addButton(withTitle: "取消")
    alert.addButton(withTitle: "重新检测并连接")
    alert.beginSheetModal(for: window) { response in
      if response == .alertSecondButtonReturn {
        request(from: presenter, connection: connection, enabled: true)
      }
    }
  }
}
