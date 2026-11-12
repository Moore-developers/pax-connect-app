import AppKit
import PaxClient
import PaxCore
import PaxUI

/// Formal entry: shared Store selection, inline list navigation and daemon limits projection.
@MainActor final class AISessionLiveLimitsView: NSView {
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

  init(store: AIUsageStore) {
    self.store = store
    limits = AISessionLimitsView(
      model: .init(sessions: [], all: Self.empty, single: [:]), options: store.sessionOptions)
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

  func refresh() {
    guard let data else { return }
    let key = data.selected.first
    let analysis = key == nil ? data.current : data.detail
    let value = analysis?.limits
    let all = data.current?.limits
    let name = key.map(title)
    var notices: [String] = []
    if analysis?.truncated == true {
      notices.append("结果超过容量限制，当前结果不代表完整统计；请缩小日期范围")
    }
    let range = store.period.interval(
      now: store.now, start: store.start, end: store.end, calendar: store.calendar)
    if renderedRange != range {
      renderedRange = range
      limits.resetListPosition()
    }
    if let retained = analysis?.retainedFrom, range.lowerBound < retained {
      notices.append("所选范围超出明细保留期，仅统计 " + AIUsageFormatting.date(retained) + " 起的保留明细")
    }
    if value?.agentsIncomplete == true {
      notices.append("部分子代理记录缺失、未关联或仍在索引，代理统计可能不完整")
    }
    if value?.summary.requests == 0 { notices.append("所选日期没有已记录用量") }
    limits.loadingStatus.isHidden = value != nil || data.error != nil || !data.limitsSupported
    let message =
      data.error
      ?? (!data.limitsSupported
        ? "后台不支持会话上限分析，请更新后台"
        : notices.isEmpty ? nil : notices.joined(separator: "；"))
    status.isHidden = message == nil
    status.apply(
      data.error == nil ? .idle : .failure, text: message ?? "",
      action: data.error == nil ? nil : "重试")
    limits.setLoading(value == nil || !data.limitsSupported)
    if rendered != value || renderedAll != all || renderedKey != key
      || renderedTitle != (name ?? "")
    {
      rendered = value
      renderedAll = all
      renderedKey = key
      renderedTitle = name ?? ""
      let allScope = all.map { AISessionLimitsMapping.scope($0, selected: false) } ?? Self.empty
      let scope =
        key == nil
        ? allScope
        : value.map { AISessionLimitsMapping.scope($0, selected: true) } ?? Self.emptyDetail
      let head = key.map { AILimitSessionHead(id: $0.id, cli: $0.cli, title: name ?? "") }
      let model = AISessionLimitsModel(
        sessions: head.map { [$0] } ?? [], all: allScope,
        single: key.map { [$0.id: scope] } ?? [:])
      limits.apply(model: model, selection: key?.id)
    }
  }

  private func title(_ key: AISessionKey) -> String {
    if let entry = data?.catalog.selected, entry.key == key {
      return AISessionPickerController.title(entry)
    }
    if let entry = data?.catalog.items.first(where: { $0.key == key }) {
      return AISessionPickerController.title(entry)
    }
    let row = data?.current?.limits?.primary.first {
      $0.cli == key.cli && $0.sessionID == key.session
    }
    return (row?.title ?? "未命名会话 · " + String(key.session.prefix(12))) + " · "
      + AIClientMark.shortName(key.cli)
  }
}
