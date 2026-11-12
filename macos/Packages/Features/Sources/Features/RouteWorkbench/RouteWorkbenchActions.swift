import AppKit
import PaxCore
import PaxUI

extension RouteWorkbenchViewController {
  func menu(_ id: String) -> NSMenu {
    let menu = NSMenu()
    menu.autoenablesItems = false
    menu.delegate = destructiveTitles
    for (title, symbol, command) in [
      (
        store.isCurrent(id) ? "当前线路" : store.isSelected(id) ? "已选线路" : "使用此线路",
        "arrow.left.arrow.right", "use"
      ),
      ("查看详情", "info.circle", "details"),
      (store.isTestingLatency(id) ? "停止测试延迟" : "测试延迟", "waveform.path.ecg", "test"),
      (store.isQueryingExit(id) ? "停止查询 IP 地区" : "查询 IP 地区", "globe", "exit"),
      (
        store.isDownloading(id) ? (store.isDownloadQueued(id) ? "取消排队" : "停止测速") : "测试下载速度",
        "arrow.down", "speed"
      ),
      ("分享二维码…", "qrcode", "share"),
      ("编辑配置", "pencil", "edit"), ("创建链式代理…", "network", "chain"),
      ("删除线路…", "trash", "remove"),
    ] {
      if command == "test" || command == "edit" { menu.addItem(.separator()) }
      let item = NSMenuItem(title: title, action: #selector(menuAction(_:)), keyEquivalent: "")
      item.target = self
      item.image = PaxRouteIcon.image(symbol)
      item.preferVisibleImage()
      item.representedObject = [id, command]
      item.isEnabled =
        !(command == "use" && (store.isCurrent(id) || store.isSelected(id)))
        && !(command == "chain" && store.info(id).chain != nil)
        && !(command == "remove" && store.isCurrent(id))
      if command == "use" { item.isEnabled = item.isEnabled && store.canSelectRoute }
      if command == "edit" || command == "remove" {
        item.isEnabled = item.isEnabled && store.canEdit
      }
      if command == "speed" { item.isEnabled = store.isDownloading(id) || store.canDownload(id) }
      if command == "test" { item.isEnabled = store.isTestingLatency(id) || store.canTest(id) }
      if command == "exit" { item.isEnabled = store.isQueryingExit(id) || store.canQueryExit(id) }
      if !store.canManage && command != "test" && command != "use" && command != "details"
        && command != "edit"
        && command != "remove" && command != "speed" && command != "exit" && command != "share"
      {
        item.isEnabled = false
        item.toolTip = "此功能尚未接入后台"
      }
      if command == "remove" { destructiveTitles.mark(item) }
      menu.addItem(item)
    }
    return menu
  }
  @objc func menuAction(_ item: NSMenuItem) {
    guard let values = item.representedObject as? [String], values.count == 2 else { return }
    let id = values[0]
    switch values[1] {
    case "use": store.perform(.use(id))
    case "test":
      if store.isTestingLatency(id) {
        store.stopTests(id)
      } else {
        store.perform(.test(id, .latency))
      }
    case "exit":
      if store.isQueryingExit(id) {
        store.stopExitQueries(id)
      } else {
        store.perform(.test(id, .exit))
      }
    case "speed":
      if store.isDownloading(id) {
        store.stopDownload(id)
      } else {
        store.perform(.test(id, .speed))
      }
    case "details", "edit":
      presentRouteModal(
        RouteDetailController(store: store, id: id, editing: values[1] == "edit", tooltip: tooltip))
    case "share": shareRoute(id)
    case "chain":
      presentRouteModal(
        RouteChainController(store: store, finalID: id) { [weak self] id in
          guard let self else { return }
          self.presentRouteModal(
            RouteDetailController(store: self.store, id: id, tooltip: self.tooltip))
        })
    case "remove":
      guard let window = view.window else { return }
      let alert = NSAlert()
      alert.messageText = "删除这条线路？"
      alert.informativeText = "不会删除分组中的其它线路。"
      alert.addButton(withTitle: "取消")
      alert.addButton(withTitle: "删除")
      alert.beginSheetModal(for: window) { [weak self] result in
        if result == .alertSecondButtonReturn { self?.store.perform(.remove(id)) }
      }
    default: break
    }
  }
  @objc func resetSorting() { store.perform(.sort(.source)) }
  @objc func refresh() {
    SubscriptionRefreshMenu.present(store: store, groupID: store.selectedGroup, from: refreshButton)
  }
  @objc func testVisible() {
    let menu = NSMenu()
    menu.autoenablesItems = false
    let scope = NSMenuItem(
      title: "当前分组 · \(store.visible.count) 条匹配线路", action: nil, keyEquivalent: "")
    scope.isEnabled = false
    menu.addItem(scope)
    menu.addItem(.separator())
    for (title, kind, symbol) in [
      ("查询 IP 地区", "exit", "globe"), ("测试延迟", "latency", "timer"),
      ("测试下载速度", "speed", "arrow.down"),
    ] {
      let item = NSMenuItem(title: title, action: #selector(testScope(_:)), keyEquivalent: "")
      item.target = self
      item.representedObject = RouteTestScope(
        kind: kind == "exit" ? .exit : kind == "speed" ? .speed : .latency, routes: store.visible)
      item.isEnabled =
        !store.visible.isEmpty
        && (kind == "exit"
          ? store.canBatchExit : kind == "speed" ? store.canBatchDownload : store.canBatchTest)
      item.image = PaxRouteIcon.image(symbol)
      item.preferVisibleImage()
      menu.addItem(item)
    }
    if store.hasLatencyTests {
      menu.addItem(.separator())
      let stop = NSMenuItem(title: "停止延迟测试", action: #selector(stopTests), keyEquivalent: "")
      stop.target = self
      menu.addItem(stop)
    }
    if store.hasExitQueries {
      let stop = NSMenuItem(
        title: "停止 IP 地区查询", action: #selector(stopExitQueries), keyEquivalent: "")
      stop.target = self
      menu.addItem(stop)
    }
    if store.hasDownload {
      menu.addItem(.separator())
      let stop = NSMenuItem(title: "停止下载测速", action: #selector(stopDownloads), keyEquivalent: "")
      stop.target = self
      menu.addItem(stop)
    }
    menu.popUp(positioning: nil, at: NSPoint(x: 0, y: testButton.bounds.maxY), in: testButton)
  }
  @objc func testScope(_ item: NSMenuItem) {
    guard let scope = item.representedObject as? RouteTestScope else { return }
    let frozen = scope.routes
    store.performTest(frozen, kind: scope.kind)
  }

  @objc func stopExitQueries() { store.stopExitQueries() }
  @objc func stopTests() { store.stopTests() }
  @objc func stopDownloads() { store.stopDownload() }
  @objc func confirmCertificates() {
    presentRouteModal(RouteTLSBatchController(store: store))
  }
  func showImport() { importRoutes() }
  @objc func importRoutes() { presentRouteModal(RouteImportController(store: store)) }
  @objc func manageGroups() {
    guard store.canManageGroups else { return }
    presentRouteModal(RouteGroupsController(store: store))
  }
}

private struct RouteTestScope {
  let kind: RouteWorkbenchStore.Test
  let routes: [NodeProfile]
}
