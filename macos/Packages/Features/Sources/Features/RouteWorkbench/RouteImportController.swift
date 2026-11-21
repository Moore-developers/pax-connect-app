import AppKit
import Observation
import PaxCore
import PaxUI

@MainActor
final class RouteImportController: RouteModalController, NSTextViewDelegate {
  override var localizedTitleKey: String? { "routes.import" }
  override var localizedSubtitleKey: String? { "modal.route.import_note" }

  private let session: RouteImportSession
  private let input = NSTextView()
  private let rows = NSStackView()
  private let results = NSScrollView()
  private var resultsHeight: NSLayoutConstraint?
  private lazy var pasteButton = button(interfaceCopy.text("app.paste"), #selector(pasteLink))
  private let loading = PaxLoadingStateView()
  init(store: RouteWorkbenchStore) {
    session = store.makeImportSession()
    super.init(
      title: "导入线路", subtitle: "分享链接、HTTPS 订阅地址、Clash YAML 或 sing-box JSON",
      symbol: "square.and.arrow.down")
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    super.loadView()
    input.isRichText = false
    input.font = PaxTheme.Typography.label()
    input.setAccessibilityLabel(self.interfaceCopy.text("action.routes.import_input_ax"))
    input.setAccessibilityIdentifier("import-input")
    input.isAutomaticQuoteSubstitutionEnabled = false
    input.isAutomaticDashSubstitutionEnabled = false
    input.delegate = self
    let scroll = NSScrollView()
    scroll.hasVerticalScroller = true
    scroll.borderType = .bezelBorder
    scroll.documentView = input
    input.frame = NSRect(
      origin: .zero,
      size: NSSize(
        width: preferredContentSize.width - PaxTheme.Spacing.page * 2,
        height: PaxTheme.Layout.galleryTable))
    input.textContainer?.widthTracksTextView = true
    scroll.heightAnchor.constraint(equalToConstant: PaxTheme.Layout.galleryTable).isActive = true
    pane.add(scroll)
    pane.add(pasteButton)
    pane.add(loading)
    rows.orientation = .vertical
    rows.alignment = .leading
    rows.spacing = PaxTheme.Spacing.tight
    results.hasVerticalScroller = true
    let document = RouteImportResultsDocument()
    document.translatesAutoresizingMaskIntoConstraints = false
    document.addSubview(rows)
    results.documentView = document
    rows.translatesAutoresizingMaskIntoConstraints = false
    NSLayoutConstraint.activate([
      document.widthAnchor.constraint(equalTo: results.contentView.widthAnchor),
      rows.leadingAnchor.constraint(equalTo: document.leadingAnchor),
      rows.trailingAnchor.constraint(equalTo: document.trailingAnchor),
      rows.topAnchor.constraint(equalTo: document.topAnchor),
      rows.bottomAnchor.constraint(equalTo: document.bottomAnchor),
    ])
    resultsHeight = results.heightAnchor.constraint(greaterThanOrEqualToConstant: 0)
    resultsHeight?.isActive = true
    pane.add(results)
    observe()
  }
  private func observe() {
    withObservationTracking {
      input.isEditable = !session.committing
      let state: PaxLoadingStateView.State
      switch session.phase {
      case .reading, .downloading, .importing: state = .loading
      case .completed: state = .success
      case .partial: state = .partial
      case .failed: state = .failure
      case .idle, .cancelled: state = .idle
      }
      loading.apply(state, message: session.message(context: interfaceCopy.context))
      if session.finished && session.phase == .completed {
        Task { @MainActor [weak self] in
          guard let self, self.session.finished, self.session.phase == .completed else { return }
          self.close()
        }
      }
      pane.status.stringValue = ""
      pane.status.isHidden = true
      pasteButton.isEnabled = !session.committing
      let close = button(
        session.busy
          ? self.interfaceCopy.text("common.cancel")
          : self.interfaceCopy.text("profile_editor.close"), #selector(close))
      close.isEnabled = !session.committing
      let retry = button(self.interfaceCopy.text("ai_usage.retry"), #selector(retryImport))
      retry.isEnabled = !session.busy && session.phase == .failed
      pane.setActions(session.phase == .failed ? [close, retry] : [close])
      for child in rows.arrangedSubviews {
        rows.removeArrangedSubview(child)
        child.removeFromSuperview()
      }
      for item in session.preview?.items ?? [] {
        guard !item.errorCode.isEmpty else { continue }
        let details: [String?] = [
          item.line > 0
            ? self.interfaceCopy.text(
              "interface_detail.50cf3a22f9", ["arg0": String(describing: item.line)]) : nil,
          item.displayName.isEmpty ? nil : item.displayName,
          item.protocolName.isEmpty ? nil : item.protocolName.uppercased(),
          ProductErrorPresentation.message(
            ProductError(code: item.errorCode), context: self.interfaceCopy.context),
        ]
        let title = details.compactMap { $0 }.joined(separator: " · ")
        let result = NSTextField(wrappingLabelWithString: title)
        result.font = PaxTheme.Typography.label()
        result.textColor = PaxTheme.Color.error
        rows.addArrangedSubview(result)
      }
      results.isHidden = rows.arrangedSubviews.isEmpty
      resultsHeight?.constant = min(
        PaxTheme.Layout.galleryTable, rows.fittingSize.height)
      pane.invalidateContentSize()
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observe() }
    }
  }
  func textDidChange(_ notification: Notification) { session.recognize(input.string) }
  @objc private func pasteLink() {
    guard !session.committing else { return }
    input.string = NSPasteboard.general.string(forType: .string) ?? ""
    session.recognize(input.string)
  }
  @objc private func retryImport() { session.recognize(input.string) }
  override func close() {
    guard !session.committing else { return }
    session.cancelImmediately()
    super.close()
  }
}

private final class RouteImportResultsDocument: NSView {
  override var isFlipped: Bool { true }
}
