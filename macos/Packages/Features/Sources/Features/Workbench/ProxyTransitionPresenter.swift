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
        RouteTLSSettingsViewController(
          store: connection, route: route, enableAfterSave: true,
          context: (presenter as? any InterfaceLanguageConsumer)?.interfaceCopy.context ?? .chinese)
      )
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
    let context = (presenter as? any InterfaceLanguageConsumer)?.interfaceCopy.context ?? .chinese
    let alert = NSAlert()
    alert.messageText =
      error.code == "pax_tun_stale"
      ? context.text("detail_controls.ff7289e0b1") : context.text("detail_controls.eca3426b18")
    alert.informativeText =
      error.code == "pax_tun_stale"
      ? context.text("detail_controls.4033ad19da")
      : ProductErrorPresentation.message(error, context: context)
    alert.addButton(withTitle: context.text("detail_controls.2cd0f3be87"))
    alert.addButton(withTitle: context.text("detail_controls.e97917e699"))
    alert.beginSheetModal(for: window) { response in
      if response == .alertSecondButtonReturn {
        request(from: presenter, connection: connection, enabled: true)
      }
    }
  }
}
