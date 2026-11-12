import AppKit
import PaxCore
import PaxUI

@MainActor
final class RouteProxyController: RouteModalController {
  override var maximumPresentationWidth: CGFloat? { PaxTheme.ProxyRoute.width }
  private let store: RouteWorkbenchStore
  private let form = RouteProxyForm(ProxyRouteDraft())
  private let onSaved: (String) -> Void
  private var submitting = false
  private var attempted: ProxyRouteDraft?
  private var requestID = ""
  private lazy var submit = button("添加线路", #selector(save), primary: true)
  private lazy var paste = button("粘贴代理信息", #selector(pasteProxy))
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
    pane.setActions([button("取消", #selector(close)), submit])
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
    submit.title = "添加中…"
    Task { [weak self] in
      guard let self else { return }
      defer {
        self.submitting = false
        self.form.setEnabled(true)
        self.submit.isEnabled = true
        self.paste.isEnabled = true
        self.submit.title = "添加线路"
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
            "保存未确认，请检查后台连接后重试；相同请求不会重复创建。\n" + ProductErrorPresentation.display(error)
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
    alert.messageText = "放弃本次填写？"
    alert.informativeText = "关闭后不会保存草稿。已受理的保存请求不会被撤销。"
    alert.addButton(withTitle: "继续填写")
    alert.addButton(withTitle: "放弃填写")
    alert.beginSheetModal(for: window) { [weak self] result in
      if result == .alertSecondButtonReturn { self?.dismissDraft() }
    }
  }
  private func dismissDraft() { super.close() }
  override func showError(_ error: Error) {
    super.showError(error)
    if let validation = error as? ProxyRouteDraft.Validation {
      pane.status.stringValue = validation.message
    }
    pane.invalidateContentSize()
  }
}
