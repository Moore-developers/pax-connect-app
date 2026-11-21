import AppKit
import PaxClient
import PaxCore
import PaxUI

/// Formal entry: shared Store selection, inline list navigation and daemon limits projection.
@MainActor final class AISessionLiveLimitsView: NSView {
  let interfaceCopy: InterfaceCopyBindings
  var context: PresentationContext { interfaceCopy.context }

  private let store: AIUsageStore
  private var data: AISessionStore? { store.data?.sessions }
  private let status = PaxInlineStatus()
  private let limits: AISessionLimitsView
  private var rendered: AISessionLimits?
  private var renderedAll: AISessionLimits?
  private var renderedTitle = ""
  private var renderedRange: Range<Date>?
  private var renderedKey: AISessionKey?
  private static let empty = AILimitScope(
    summary: .init(
      input: .init(), overSpend: .init(), sessions: 0, overSessions: 0,
      requests: 0, turns: 0, turnsComplete: true, subagents: 0),
    lists: [.init(kind: .sessions, rows: []), .init(kind: .models, rows: [])])

  private static let emptyDetail = AILimitScope(
    summary: empty.summary,
    lists: [.init(kind: .main, rows: []), .init(kind: .subagents, rows: [])])

  init(store: AIUsageStore, interfaceCopy: InterfaceCopyBindings = InterfaceCopyBindings()) {
    self.interfaceCopy = interfaceCopy
    let context = interfaceCopy.context

    self.store = store
    limits = AISessionLimitsView(
      model: .init(sessions: [], all: Self.empty, single: [:]), options: store.sessionOptions,
      interfaceCopy: interfaceCopy)
    super.init(frame: .zero)
    setAccessibilityIdentifier("ai-session-analysis")
    limits.onSelect = { [weak self] id in
      guard let self else { return }
      if let id {
        let row = data?.current?.limits?.primary.first { "\($0.cli):\($0.sessionID)" == id }
        guard let row else { return }
        data?.choose([.init(cli: row.cli, session: row.sessionID)])
      } else {
        data?.choose([])
      }
      refresh()
    }
    status.onAction = { [weak self] in self?.data?.refresh() }
    let body = AIUsageLayout.stack([status, limits])
    body.spacing = PaxTheme.Spacing.regular
    body.translatesAutoresizingMaskIntoConstraints = false
    addSubview(body)
    NSLayoutConstraint.activate([
      body.leadingAnchor.constraint(equalTo: leadingAnchor),
      body.trailingAnchor.constraint(equalTo: trailingAnchor),
      body.topAnchor.constraint(equalTo: topAnchor),
      body.bottomAnchor.constraint(equalTo: bottomAnchor),
    ])
    refresh()
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  func invalidateLanguage() {
    rendered = nil
    limits.invalidateLanguage()
    refresh()
  }
  func refresh() {
    guard let data else { return }
    let key = data.selected.first
    let analysis = key == nil ? data.current : data.detail
    let value = analysis?.limits
    let all = data.current?.limits
    let name = key.map(title)
    var notices: [String] = []
    if analysis?.truncated == true {
      notices.append(context.text("session_limits.d41ba7e60d"))
    }
    let range = store.period.interval(
      now: store.now, start: store.start, end: store.end, calendar: store.calendar)
    if renderedRange != range {
      renderedRange = range
      limits.resetListPosition()
    }
    if let retained = analysis?.retainedFrom, range.lowerBound < retained {
      notices.append(
        context.text("session_limits.c6de01a319") + AIUsageFormatting.date(retained)
          + context.text("session_limits.0b88c067e2"))
    }
    if value?.agentsIncomplete == true {
      notices.append(context.text("session_limits.4372bdd28e"))
    }
    if value?.summary.requests == 0 { notices.append(context.text("session_limits.288b9bf9b1")) }
    limits.loadingStatus.isHidden = value != nil || data.error != nil || !data.limitsSupported
    let message =
      data.error
      ?? (!data.limitsSupported
        ? context.text("session_limits.c7a455453e")
        : notices.isEmpty ? nil : notices.joined(separator: "；"))
    status.isHidden = message == nil
    status.apply(
      data.error == nil ? .idle : .failure, text: message ?? "",
      action: data.error == nil ? nil : context.text("session_limits.b8784c8dd5"))
    limits.setLoading(value == nil || !data.limitsSupported)
    if rendered != value || renderedAll != all || renderedKey != key
      || renderedTitle != (name ?? "")
    {
      rendered = value
      renderedAll = all
      renderedKey = key
      renderedTitle = name ?? ""
      let allScope =
        all.map { AISessionLimitsMapping.scope($0, selected: false, context: context) }
        ?? Self.empty
      let scope =
        key == nil
        ? allScope
        : value.map { AISessionLimitsMapping.scope($0, selected: true, context: context) }
          ?? Self.emptyDetail
      let head = key.map { AILimitSessionHead(id: $0.id, cli: $0.cli, title: name ?? "") }
      let model = AISessionLimitsModel(
        sessions: head.map { [$0] } ?? [], all: allScope,
        single: key.map { [$0.id: scope] } ?? [:])
      limits.apply(model: model, selection: key?.id)
    }
  }

  private func title(_ key: AISessionKey) -> String {
    if let entry = data?.catalog.selected, entry.key == key {
      return AISessionPickerController.title(entry, context: context)
    }
    if let entry = data?.catalog.items.first(where: { $0.key == key }) {
      return AISessionPickerController.title(entry, context: context)
    }
    let row = data?.current?.limits?.primary.first {
      $0.cli == key.cli && $0.sessionID == key.session
    }
    return
      (row.map { AISessionLimitsMapping.title($0, context: context) } ?? context.text(
        "session_limits.508c23d5d7") + String(key.session.prefix(12)))
      + " · "
      + AIClientMark.shortName(key.cli)
  }
}
