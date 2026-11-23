import AppKit
import PaxClient
import PaxCore
import PaxUI

@MainActor
final class AIProfileHistoryController: WorkbenchModalController {
  private let store: AIProfilesStore
  private let client: PaxClient.AIClient
  private let actions = AIUsageActions()
  private var page: AIProfileHistoryPage?
  private var before: AIProfileHistoryPage.Cursor?
  private var requestedCursor: AIProfileHistoryPage.Cursor?
  private var task: Task<Void, Never>?
  private var failed = false
  var onReveal: ((String) -> Void)?

  init(store: AIProfilesStore, client: PaxClient.AIClient) {
    self.store = store
    self.client = client
    super.init(title: "", size: .init(width: 540, height: 500), symbol: "clock.arrow.circlepath")
  }

  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  override var maximumPresentationWidth: CGFloat? { 560 }

  override func loadView() {
    super.loadView()
    interfaceCopy.onChange = { [weak self] in self?.render() }
    load(before: nil)
  }

  override func viewWillDisappear() {
    super.viewWillDisappear()
    task?.cancel()
  }

  private func load(before cursor: AIProfileHistoryPage.Cursor?) {
    guard task == nil else { return }
    failed = false
    requestedCursor = cursor
    task = Task { [weak self, store, client] in
      do {
        let next = try await store.clientHistory(client, before: cursor)
        guard let self, !Task.isCancelled else { return }
        self.page = next
        self.before = cursor
      } catch {
        guard let self, !Task.isCancelled else { return }
        self.failed = true
      }
      self?.task = nil
      self?.render()
    }
    render()
  }

  private func render() {
    let context = interfaceCopy.context
    pane.applyTitle(context.text("ai_workbench.client_history", ["client": client.rawValue]))
    pane.clear()
    actions.reset()
    pane.add(AIUsageLayout.label(context.text("ai_workbench.history_scope"), secondary: true))
    if let page, !page.events.isEmpty {
      pane.add(
        PaxPreferenceGroup(
          title: context.text("ai_workbench.history"), symbol: "clock",
          rows: page.events.map(eventRow)))
    } else {
      let key =
        task != nil
        ? "ai_workbench.history_loading"
        : failed ? "ai_workbench.history_unavailable" : "ai_workbench.history_empty"
      pane.add(AIUsageLayout.label(context.text(key), secondary: true))
    }

    var footer: [NSButton] = []
    if before != nil {
      footer.append(
        actions.button(context.text("ai_workbench.history_latest"), enabled: task == nil) {
          [weak self] in self?.load(before: nil)
        })
    }
    if failed {
      if page?.events.isEmpty == false {
        pane.add(
          AIUsageLayout.label(context.text("ai_workbench.history_unavailable"), secondary: true))
      }
      footer.append(
        actions.button(context.text("ai_workbench.history_retry")) { [weak self] in
          guard let self else { return }
          self.load(before: self.requestedCursor)
        })
    }
    if let next = page?.next {
      footer.append(
        actions.button(context.text("ai_workbench.history_older"), enabled: task == nil) {
          [weak self] in self?.load(before: next)
        })
    }
    pane.setActions(footer)
  }

  private func eventRow(_ event: AIProfileHistoryPage.Event) -> NSView {
    let context = interfaceCopy.context
    let available = store.workspace.profiles.contains { $0.id == event.profileID }
    let title = event.profileName.isEmpty ? event.source.label(context: context) : event.profileName
    let action = context.text(
      event.wasApplied ? "ai_workbench.history_applied" : "ai_workbench.history_observed")
    let date = TrafficFormatting.dateMinute(event.date)
    let suffix =
      !event.profileID.isEmpty && !available
      ? " · " + context.text("ai_workbench.history_removed") : ""
    let control: NSView
    if available {
      control = actions.button(context.text("ai_workbench.history_reveal")) { [weak self] in
        guard let self else { return }
        let reveal = self.onReveal
        self.onDismissed = { reveal?(event.profileID) }
        self.close()
      }
    } else {
      let placeholder = NSView()
      placeholder.widthAnchor.constraint(equalToConstant: 0).isActive = true
      control = placeholder
    }
    return PaxPreferenceRow(title: title, detail: date + " · " + action + suffix, control: control)
  }
}
