import AppKit
import PaxCore
import PaxUI

/// A frozen failure list is confirmed once; daemon atomically rechecks every identity.
@MainActor
final class RouteTLSBatchController: RouteModalController {
  override var localizedTitleKey: String? { "modal.route.tls_failed" }
  override var localizedSubtitleKey: String? { "modal.route.tls_skip_note" }

  private let store: RouteWorkbenchStore
  private let frozen: [NodeProfile]
  private var task: Task<Void, Never>?
  init(store: RouteWorkbenchStore) {
    self.store = store
    frozen = store.certificateFailures
    super.init(title: "证书校验失败", subtitle: "仅对列出的线路跳过并重新测量", symbol: "exclamationmark.shield")
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    super.loadView()
    pane.add(
      NSTextField(wrappingLabelWithString: self.interfaceCopy.text("interface_detail.41b05f5224"))
    )
    for route in frozen {
      let reason =
        route.testSummary?.error.map(ProductErrorPresentation.message)
        ?? self.interfaceCopy.text("flow.certificate_failed")
      pane.add(
        NSTextField(
          wrappingLabelWithString:
            "\(route.name) · \(route.protocolName.uppercased())\n\(route.server):\(route.port) · SNI \(route.serverName)\n\(reason)"
        ))
    }
    actions(false)
  }
  private func actions(_ busy: Bool) {
    let cancel = button(self.interfaceCopy.text("common.cancel"), #selector(close))
    cancel.isEnabled = !busy
    let save = button(
      self.interfaceCopy.text("action.routes.confirm_skip_retest"), #selector(confirm),
      primary: true)
    save.isEnabled = !busy && store.canConfirmCertificateFailures
    save.setAccessibilityIdentifier("confirm-tls-batch")
    pane.setActions([cancel, save])
  }
  @objc private func confirm() {
    guard task == nil, store.canConfirmCertificateFailures else { return }
    actions(true)
    task = Task { [weak self, store, frozen] in
      do {
        try await store.confirmCertificateFailures(frozen)
        self?.task = nil
        self?.close()
      } catch {
        self?.task = nil
        self?.showError(error)
        self?.actions(false)
      }
    }
  }
  override func close() {
    guard task == nil else { return }
    super.close()
  }
}
