import AppKit
import PaxCore
import PaxUI

@MainActor
final class RouteChainController: RouteModalController {
  private let store: RouteWorkbenchStore
  private let finalID: String
  private let completed: (String) -> Void
  private let fronts = PaxRoutePicker()
  private let name = PaxRouteField("")
  private let preview = NSStackView()
  private var proposedName = ""
  private var choices: [NodeProfile] = []
  private var frozenFinal: NodeProfile?
  private var saving = false
  init(store: RouteWorkbenchStore, finalID: String, completed: @escaping (String) -> Void) {
    self.store = store
    self.finalID = finalID
    self.completed = completed
    super.init(title: "创建链式代理", subtitle: "前置连接 → 最终出口", symbol: "network", tone: .purple)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    super.loadView()
    fronts.addItem(withTitle: "选择已有线路…")
    choices = store.eligibleFronts(final: finalID)
    frozenFinal = store.node(finalID)
    fronts.addItems(withTitles: choices.map(\.name))
    fronts.target = self
    fronts.action = #selector(changed)
    fronts.font = PaxTheme.Typography.label(size: PaxTheme.Routes.bodyFont)
    let final = NSStackView(views: [
      PaxRouteIcon.view("server.rack"), PaxRouteText.label(store.node(finalID)?.name ?? "不可用"),
      PaxFeatureTag(store.node(finalID)?.protocolName.uppercased() ?? "", style: .protocolLabel),
    ])
    pane.add(
      PaxFormGrid(
        rows: [
          [
            PaxRouteText.label("前置线路", font: PaxTheme.Typography.caption(), secondary: true),
            fronts,
          ],
          [
            PaxRouteText.label("最终线路", font: PaxTheme.Typography.caption(), secondary: true), final,
          ],
          [
            PaxRouteText.label("组合线路名称", font: PaxTheme.Typography.caption(), secondary: true),
            name,
          ],
        ], style: .labelled))
    preview.orientation = .vertical
    preview.alignment = .leading
    pane.add(preview)
    pane.add(
      PaxRouteText.label(
        "创建一条组合线路，保留原线路。创建后不会切换当前线路。", font: PaxTheme.Typography.caption(), secondary: true,
        wrapping: true))
    pane.setActions([
      button("取消", #selector(close)), button("创建", #selector(create), primary: true),
    ])
  }
  @objc private func changed() {
    let routes = choices
    let index = fronts.indexOfSelectedItem - 1
    guard routes.indices.contains(index), let final = store.node(finalID) else { return }
    let front = routes[index]
    if name.stringValue.isEmpty || name.stringValue == proposedName {
      name.stringValue = front.name + " → " + final.name
    }
    proposedName = front.name + " → " + final.name
    for child in preview.arrangedSubviews {
      preview.removeArrangedSubview(child)
      child.removeFromSuperview()
    }
    let view = PaxConnectionPath(nodes: store.path(frontID: front.id, finalID: finalID))
    preview.addArrangedSubview(view)
    view.widthAnchor.constraint(equalTo: preview.widthAnchor).isActive = true
    pane.invalidateContentSize()
  }
  @objc private func create() {
    guard !saving else { return }
    let routes = choices
    let index = fronts.indexOfSelectedItem - 1
    guard routes.indices.contains(index) else {
      showError(RouteWorkbenchStore.EditError("请选择前置线路"))
      return
    }
    if store.isLive {
      saving = true
      pane.status.stringValue = "正在创建…"
      Task { [weak self] in
        guard let self else { return }
        do {
          let id = try await self.store.createChainLive(
            front: routes[index].id, final: self.finalID, name: self.name.stringValue,
            frozenFront: routes[index], frozenFinal: self.frozenFinal)
          self.close()
          self.completed(id)
        } catch {
          self.saving = false
          self.showError(error)
        }
      }
      return
    }
    do {
      let id = try store.createChain(
        front: routes[index].id, final: finalID, name: name.stringValue)
      let completed = self.completed
      close()
      completed(id)
    } catch { showError(error) }
  }
}
