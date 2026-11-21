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
        store.isCurrent(id)
          ? self.interfaceCopy.text("routes.current")
          : store.isSelected(id)
            ? self.interfaceCopy.text("routes.selected")
            : self.interfaceCopy.text("interface_detail.805482461e"),
        "arrow.left.arrow.right", "use"
      ),
      (self.interfaceCopy.text("interface_detail.a748cc074f"), "info.circle", "details"),
      (
        store.isTestingLatency(id)
          ? self.interfaceCopy.text("interface_detail.e61a02b556")
          : self.interfaceCopy.text("action.routes.test_latency"), "waveform.path.ecg", "test"
      ),
      (
        store.isQueryingExit(id)
          ? self.interfaceCopy.text("interface_detail.9a3898b764")
          : self.interfaceCopy.text("action.routes.lookup_exit"), "globe", "exit"
      ),
      (
        store.isDownloading(id)
          ? (store.isDownloadQueued(id)
            ? self.interfaceCopy.text("interface_detail.16c1f9b337")
            : self.interfaceCopy.text("interface_detail.4a372f90da"))
          : self.interfaceCopy.text("action.routes.test_download"),
        "arrow.down", "speed"
      ),
      (self.interfaceCopy.text("interface_detail.9397f2ba6f"), "qrcode", "share"),
      (self.interfaceCopy.text("interface_detail.b2b5b9fafa"), "pencil", "edit"),
      (self.interfaceCopy.text("interface_detail.ea3ab3e97a"), "network", "chain"),
      (self.interfaceCopy.text("interface_detail.e7d3d4b526"), "trash", "remove"),
    ] {
      if command == "test" || command == "edit" { menu.addItem(.separator()) }
      let item = NSMenuItem(title: title, action: #selector(menuAction(_:)), keyEquivalent: "")
      item.target = self
      item.image = PaxRouteIcon.image(symbol)
      item.preferVisibleImage()
      item.representedObject = [id, command]
      item.isEnabled =
        !(command == "use" && (store.isCurrent(id) || store.isSelected(id)))
        && !(command == "chain" && store.info(id, context: self.interfaceCopy.context).chain != nil)
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
        item.toolTip = self.interfaceCopy.text("interface_detail.081d60fb80")
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
      alert.messageText = self.interfaceCopy.text("interface_detail.4ab782b36d")
      alert.informativeText = self.interfaceCopy.text("interface_detail.77e28591dc")
      alert.addButton(withTitle: self.interfaceCopy.text("common.cancel"))
      alert.addButton(withTitle: self.interfaceCopy.text("ai_usage.delete"))
      alert.beginSheetModal(for: window) { [weak self] result in
        if result == .alertSecondButtonReturn { self?.store.perform(.remove(id)) }
      }
    default: break
    }
  }
  @objc func resetSorting() { store.perform(.sort(.source)) }
  @objc func refresh() {
    SubscriptionRefreshMenu.present(
      store: store, groupID: store.selectedGroup, from: refreshButton,
      context: self.interfaceCopy.context)
  }
  @objc func testVisible() {
    let menu = NSMenu()
    menu.autoenablesItems = false
    let scope = NSMenuItem(
      title: self.interfaceCopy.text(
        "interface_detail.d38b2d02d3", ["arg0": String(describing: store.visible.count)]),
      action: nil, keyEquivalent: "")
    scope.isEnabled = false
    menu.addItem(scope)
    menu.addItem(.separator())
    for (title, kind, symbol) in [
      (self.interfaceCopy.text("action.routes.lookup_exit"), "exit", "globe"),
      (self.interfaceCopy.text("action.routes.test_latency"), "latency", "timer"),
      (self.interfaceCopy.text("action.routes.test_download"), "speed", "arrow.down"),
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
      let stop = NSMenuItem(
        title: self.interfaceCopy.text("action.routes.stop_latency"), action: #selector(stopTests),
        keyEquivalent: "")
      stop.target = self
      menu.addItem(stop)
    }
    if store.hasExitQueries {
      let stop = NSMenuItem(
        title: self.interfaceCopy.text("action.routes.stop_exit"),
        action: #selector(stopExitQueries), keyEquivalent: "")
      stop.target = self
      menu.addItem(stop)
    }
    if store.hasDownload {
      menu.addItem(.separator())
      let stop = NSMenuItem(
        title: self.interfaceCopy.text("action.routes.stop_download"),
        action: #selector(stopDownloads), keyEquivalent: "")
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
