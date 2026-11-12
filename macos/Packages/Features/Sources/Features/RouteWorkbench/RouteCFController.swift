import AppKit
import Observation
import PaxCore
import PaxUI

/// 候选测试仅存在此编辑窗口；取消任务不应用 IP，明确使用后返回父编辑草稿。
@MainActor
final class RouteCFController: RouteModalController {
  static func make(
    store: RouteWorkbenchStore, draft: RouteWorkbenchStore.Draft,
    apply: @escaping (RouteWorkbenchStore.Candidate) -> Void
  ) -> RouteModalController {
    if store.hasCFWorkspace {
      return CFWorkspaceController(store: store, draft: draft, apply: apply)
    }
    return RouteCFController(store: store, draft: draft, apply: apply)
  }
  private let store: RouteWorkbenchStore
  private let draft: RouteWorkbenchStore.Draft
  private var candidates: [RouteWorkbenchStore.Candidate]
  private var selected: String
  private let configured: String
  private let domain: String
  private let apply: (RouteWorkbenchStore.Candidate) -> Void
  private var task: Task<Void, Never>?
  private var generation = UUID()
  private var busy = false

  private let table = PaxCandidateTable(titles: ["IP 地址", "入口延迟", "测试时间", "状态"], choices: true)
  private var tableHeight: NSLayoutConstraint?
  private var testButton: PaxRouteButton?
  private var allButton: PaxRouteButton?
  private var useButton: PaxRouteButton?
  init(
    store: RouteWorkbenchStore, draft: RouteWorkbenchStore.Draft,
    apply: @escaping (RouteWorkbenchStore.Candidate) -> Void
  ) {
    self.store = store
    self.draft = draft
    candidates = store.isLive ? [] : store.candidates(for: draft)
    selected = draft.server
    configured = draft.server
    domain = draft.host.isEmpty ? draft.sni : draft.host
    self.apply = apply
    super.init(title: "选择 CF IP", subtitle: "关联域名：" + domain, symbol: "cloud")
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    super.loadView()
    testButton = button("测试所选", #selector(testSelected))
    allButton = button("优选全部", #selector(testAll))
    let tools = NSStackView(views: [
      button("添加 IP", #selector(addIP)), testButton ?? NSView(), allButton ?? NSView(),
    ])
    tools.spacing = PaxTheme.Spacing.tight
    pane.add(tools)
    table.onSelect = { [weak self] id in
      self?.selected = id
      self?.render()
    }
    tableHeight = table.heightAnchor.constraint(greaterThanOrEqualToConstant: 0)
    tableHeight?.isActive = true
    pane.add(table)
    pane.add(
      PaxRouteText.label(
        "测试当前域名与传输的连接结果；使用后填回服务器地址，保存配置才生效。", font: PaxTheme.Typography.caption(), secondary: true,
        wrapping: true))
    let use = button("使用此 IP", #selector(useIP), primary: true)
    useButton = use
    pane.setActions([button("取消", #selector(close)), use])
    render()
    observeNetwork()
    if store.isLive {
      busy = true
      render()
      task = Task { [weak self, store] in
        do {
          let values = try await store.fetchCFCandidates()
          guard !Task.isCancelled, let self else { return }
          self.candidates = values
          if draft.cf, RouteWorkbenchStore.validIP(draft.server),
            !self.candidates.contains(where: { $0.ip == draft.server })
          {
            self.candidates.insert(.init(ip: draft.server, measurement: .init()), at: 0)
          }
          if !self.candidates.contains(where: { $0.ip == self.selected }), let first = values.first
          {
            self.selected = first.ip
          }
          self.busy = false
          self.task = nil
          self.render()
        } catch {
          guard !Task.isCancelled else { return }
          self?.busy = false
          self?.task = nil
          self?.showError(error)
          self?.render()
        }
      }
    }
  }
  private func render() {
    testButton?.title = busy ? "停止测试" : "测试所选"
    allButton?.isEnabled = !busy
    table.apply(
      rows: candidates.map { candidate in
        let result = candidate.measurement
        return .init(
          id: candidate.ip,
          values: [
            candidate.ip, result.display(.cf), result.time ?? "—",
            result.running ? "测试中…" : result.failure ?? (result.value == nil ? "未测试" : "可连接"),
          ],
          statusColor: result.failure != nil
            ? PaxTheme.Color.error
            : result.value == nil ? PaxTheme.Routes.muted : PaxTheme.Color.healthy,
          statusSymbol: result.running
            ? "clock"
            : result.failure != nil
              ? "exclamationmark.circle"
              : result.value == nil ? "minus.circle" : "checkmark.circle",
          numericValues: result.value.map { [1: $0] } ?? [:])
      }, selected: selected, configured: configured)
    tableHeight?.constant = min(
      PaxTheme.Workbench.fullTable,
      CGFloat(candidates.count) * PaxTheme.Table.compactRow + PaxTheme.Table.compactHeader)
    useButton?.isEnabled =
      !busy
      && candidates.contains {
        $0.ip == selected
          && (!store.isLive || canUse($0))
      }
    pane.invalidateContentSize()
  }
  private func canUse(_ candidate: RouteWorkbenchStore.Candidate) -> Bool {
    candidate.measurement.value != nil && candidate.measurement.failure == nil
      && candidate.network == store.physicalNetwork
      && (candidate.validUntil.map { $0 > Date() } ?? false)
  }
  private func observeNetwork() {
    guard store.isLive else { return }
    withObservationTracking {
      _ = store.physicalNetwork
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in
        guard let self else { return }
        for i in self.candidates.indices
        where self.candidates[i].network != self.store.physicalNetwork {
          self.candidates[i].measurement = .init(failure: "网络已变化，请重新测试")
          self.candidates[i].validUntil = nil
        }
        self.render()
        self.observeNetwork()
      }
    }
  }
  @objc private func addIP() {
    guard let window = view.window else { return }
    let alert = NSAlert()
    alert.messageText = "添加 CF IP"
    alert.addButton(withTitle: "添加")
    alert.addButton(withTitle: "取消")
    let input = NSTextField(
      frame: NSRect(
        origin: .zero,
        size: NSSize(width: PaxTheme.Workbench.identityColumn, height: PaxTheme.Control.height)))
    input.placeholderString = "IPv4 或 IPv6"
    alert.accessoryView = input
    alert.beginSheetModal(for: window) { [weak self] response in
      guard let self, response == .alertFirstButtonReturn else { return }
      let ip = input.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
      guard RouteWorkbenchStore.validIP(ip), !self.candidates.contains(where: { $0.ip == ip })
      else {
        self.showError(RouteWorkbenchStore.EditError("请输入有效且不重复的 IP"))
        return
      }
      self.candidates.append(.init(ip: ip, measurement: .init()))
      self.selected = ip
      self.render()
    }
  }
  @objc private func testSelected() {
    if busy {
      stop()
      return
    }
    start(all: false)
  }
  @objc private func testAll() { start(all: true) }
  private func stop() {
    task?.cancel()
    task = nil
    busy = false
    generation = UUID()
    for index in candidates.indices { candidates[index].measurement.running = false }
    render()
  }
  private func start(all: Bool) {
    if store.isLive {
      startLive(all: all)
      return
    }
    guard !busy else { return }
    let ids = candidates.indices.filter { all || candidates[$0].ip == selected }
    guard !ids.isEmpty else { return }
    busy = true
    generation = UUID()
    let token = generation
    render()
    task = Task { [weak self] in
      for offset in stride(from: 0, to: ids.count, by: 2) {
        guard let self, self.generation == token else { return }
        let batch = Array(ids[offset..<min(offset + 2, ids.count)])
        for index in batch { self.candidates[index].measurement.running = true }
        self.render()
        do { try await Task.sleep(for: PaxTheme.Routes.testDuration) } catch { return }
        guard self.generation == token else { return }
        for index in batch {
          let failed = self.candidates[index].ip == "162.159.36.1"
          self.candidates[index].measurement = .init(
            value: failed ? nil : Double(30 + index * 13), failure: failed ? "连接超时" : nil,
            time: "刚刚")
        }
        self.render()
      }
      guard let self, self.generation == token else { return }
      self.busy = false
      self.task = nil
      if all,
        let best = self.candidates.filter({ $0.measurement.value != nil }).min(by: {
          ($0.measurement.value ?? .infinity) < ($1.measurement.value ?? .infinity)
        })
      {
        self.selected = best.ip
      }
      self.render()
    }
  }
  private func startLive(all: Bool) {
    guard !busy else { return }
    let ids = candidates.indices.filter { all || candidates[$0].ip == selected }
    guard !ids.isEmpty else { return }
    busy = true
    generation = UUID()
    let token = generation
    render()
    task = Task { [weak self, store, draft] in
      for offset in stride(from: 0, to: ids.count, by: 2) {
        guard let self, self.generation == token, !Task.isCancelled else { return }
        let indices = Array(ids[offset..<min(offset + 2, ids.count)])
        let targets = indices.map { (index: $0, ip: self.candidates[$0].ip) }
        for i in indices { self.candidates[i].measurement.running = true }
        self.render()
        await withTaskGroup(of: (Int, RouteWorkbenchStore.Candidate?, String?).self) { group in
          for target in targets {
            group.addTask {
              do {
                return (target.index, try await store.validateCF(draft, ip: target.ip), nil)
              } catch { return (target.index, nil, ProductErrorPresentation.display(error)) }
            }
          }
          for await (index, value, error) in group {
            guard self.generation == token, !Task.isCancelled else {
              group.cancelAll()
              return
            }
            if let value {
              self.candidates[index] = value
            } else {
              self.candidates[index].measurement = .init(failure: error ?? "CF 测试失败")
            }
            self.render()
          }
        }
      }
      guard let self, self.generation == token else { return }
      self.busy = false
      self.task = nil
      if all,
        let best = self.candidates.filter({
          $0.measurement.value != nil && $0.measurement.failure == nil
        }).min(by: { ($0.measurement.value ?? .infinity) < ($1.measurement.value ?? .infinity) })
      {
        self.selected = best.ip
      }
      self.render()
    }
  }
  @objc private func useIP() {
    guard !busy, let row = candidates.first(where: { $0.ip == selected }) else { return }
    guard !store.isLive || canUse(row) else {
      showError(RouteWorkbenchStore.EditError("验证已过期或网络已变化，请重新测试"))
      return
    }
    let apply = self.apply
    close()
    apply(row)
  }
  override func close() {
    stop()
    super.close()
  }
}
