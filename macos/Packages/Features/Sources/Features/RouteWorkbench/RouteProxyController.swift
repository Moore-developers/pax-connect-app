import AppKit
import PaxCore
import PaxUI

@MainActor
final class RouteProxyController: RouteModalController {
  override var localizedTitleKey: String? { "routes.create" }

  override var maximumPresentationWidth: CGFloat? { PaxTheme.ProxyRoute.width }
  private let store: RouteWorkbenchStore
  private lazy var form = RouteProxyForm(ProxyRouteDraft(), context: interfaceCopy.context)
  private let onSaved: (String) -> Void
  private var submitting = false
  private var attempted: ProxyRouteDraft?
  private var requestID = ""
  private lazy var submit = button(
    interfaceCopy.text("dashboard_value.add_route_action"), #selector(save), primary: true)
  private lazy var paste = button(
    interfaceCopy.text("proxy_validation.paste"), #selector(pasteProxy))
  init(store: RouteWorkbenchStore, onSaved: @escaping (String) -> Void) {
    self.store = store
    self.onSaved = onSaved
    super.init(title: "新增线路", symbol: "plus")
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    super.loadView()
    pane.add(paste)
    pane.add(form)
    form.onResize = { [weak self] in self?.pane.invalidateContentSize() }
    submit.keyEquivalent = "\r"
    submit.setAccessibilityIdentifier("proxy-save")
    pane.setActions([button(self.interfaceCopy.text("common.cancel"), #selector(close)), submit])
  }
  @objc private func pasteProxy() {
    do {
      let value = try ProxyRouteDraft.pasted(
        NSPasteboard.general.string(forType: .string) ?? "", keeping: form.value())
      form.apply(value)
      pane.status.isHidden = true
      pane.invalidateContentSize()
    } catch { showError(error) }
  }
  @objc private func save() {
    guard !submitting else { return }
    let value = form.value()
    do { _ = try value.document() } catch {
      showError(error)
      return
    }
    if attempted != value {
      requestID = UUID().uuidString
      attempted = value
    }
    submitting = true
    form.setEnabled(false)
    submit.isEnabled = false
    paste.isEnabled = false
    submit.title = self.interfaceCopy.text("interface_detail.f470193bcd")
    Task { [weak self] in
      guard let self else { return }
      defer {
        self.submitting = false
        self.form.setEnabled(true)
        self.submit.isEnabled = true
        self.paste.isEnabled = true
        self.submit.title = self.interfaceCopy.text("dashboard_value.add_route_action")
      }
      do {
        let result = try await self.store.createProxy(value, requestID: self.requestID)
        self.onSaved(result)
        self.dismissDraft()
      } catch {
        if let validation = error as? ProxyRouteDraft.Validation {
          self.showError(validation)
        } else if let saved = error as? RoutesStore.DocumentSaveError, case .rejected = saved {
          self.showError(saved)
        } else {
          self.pane.status.stringValue =
            self.interfaceCopy.text("interface_detail.d19a4e0ed2")
            + ProductErrorPresentation.display(error, context: self.interfaceCopy.context)
          self.pane.status.isHidden = false
          self.pane.status.superview?.isHidden = false
        }
      }
    }
  }
  override func close() {
    guard !submitting else { return }
    if form.value() == ProxyRouteDraft() {
      super.close()
      return
    }
    guard let window = view.window else { return }
    let alert = NSAlert()
    alert.messageText = self.interfaceCopy.text("interface_detail.6349cf8991")
    alert.informativeText = self.interfaceCopy.text("interface_detail.dd7eeeb97b")
    alert.addButton(withTitle: self.interfaceCopy.text("interface_detail.446f8327bf"))
    alert.addButton(withTitle: self.interfaceCopy.text("interface_detail.9ee74f5bba"))
    alert.beginSheetModal(for: window) { [weak self] result in
      if result == .alertSecondButtonReturn { self?.dismissDraft() }
    }
  }
  private func dismissDraft() { super.close() }
  override func showError(_ error: Error) {
    super.showError(error)
    pane.invalidateContentSize()
  }
}
