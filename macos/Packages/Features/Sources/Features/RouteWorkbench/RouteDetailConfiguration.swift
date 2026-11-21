import AppKit
import PaxCore
import PaxUI

extension RouteDetailController {
  func configuration() {
    guard let draft, let node = store.node(id) else { return }
    let connection = store.connection(draft)
    if let node = store.node(id), ["socks", "http"].contains(node.protocolName) {
      do {
        let form = RouteProxyForm(
          try ProxyRouteDraft(document: draft.json, name: draft.name), editing: true,
          context: self.interfaceCopy.context)
        form.onResize = { [weak self] in self?.pane.invalidateContentSize() }
        proxyForm = form
        section(
          self.interfaceCopy.text("interface_detail.48b970b568"), symbol: "slider.horizontal.3",
          views: [form])
      } catch { showError(error) }
      return
    }
    section(
      self.interfaceCopy.text("interface_detail.328fb2fbfd"), symbol: "info.circle",
      views: [
        configurationField(
          self.interfaceCopy.text("interface_detail.2035432e8a"), "name", draft.name)
      ])
    if let chain = store.info(id, context: self.interfaceCopy.context).chain {
      let popup = PaxRoutePicker()
      popup.addItems(withTitles: frontChoices.map(\.name))
      popup.identifier = NSUserInterfaceItemIdentifier("chain-front")
      let routes = frontChoices
      if let index = routes.firstIndex(where: { $0.id == draft.frontID }) {
        popup.selectItem(at: index)
      }
      popup.target = self
      popup.action = #selector(frontChanged(_:))
      section(
        self.interfaceCopy.text("action.traffic.connection_path"),
        symbol: "point.3.connected.trianglepath.dotted",
        views: [
          PaxSectionHeader(self.interfaceCopy.text("action.routes.front_route")), popup,
          PaxRouteText.label(
            self.interfaceCopy.text("interface_detail.d51deefa0d")
              + (store.node(chain.final)?.name
                ?? self.interfaceCopy.text("interface_detail.460b3574e4"))),
          PaxConnectionPath(
            nodes: store.path(id, frontID: draft.frontID, context: self.interfaceCopy.context)),
        ])
      return
    }
    let server = field(
      self.interfaceCopy.text("action.routes.server_address"), value: draft.server,
      identifier: "server")
    inputs["server"] = server.1
    server.1.delegate = self
    var serverView: NSView = server.0
    if connection.supportsCF {
      let choose = button(self.interfaceCopy.text("action.cf.choose_ip"), #selector(chooseCF))
      choose.isEnabled = !store.isLive || store.hasCFCandidates
      choose.setAccessibilityIdentifier("choose-cf-ip")
      choose.image = PaxRouteIcon.image("cloud")
      choose.imagePosition = .imageLeading
      // Label above address row.
      let label = server.0.arrangedSubviews.first
      server.0.removeArrangedSubview(server.1)
      server.1.removeFromSuperview()
      let line = NSStackView(views: [server.1, choose])
      line.spacing = PaxTheme.Spacing.tight
      let vertical = NSStackView()
      vertical.orientation = .vertical
      vertical.alignment = .leading
      vertical.spacing = PaxTheme.Spacing.tight
      if let label {
        server.0.removeArrangedSubview(label)
        label.removeFromSuperview()
        vertical.addArrangedSubview(label)
      }
      vertical.addArrangedSubview(line)
      line.widthAnchor.constraint(equalTo: vertical.widthAnchor).isActive = true
      let enabled = NSButton(
        checkboxWithTitle: self.interfaceCopy.text("action.cf.use_entry"), target: self,
        action: #selector(cfEnabled(_:)))
      enabled.state = draft.cf ? .on : .off
      enabled.font = PaxTheme.Typography.caption()
      vertical.addArrangedSubview(enabled)
      serverView = vertical
    }
    let port = configurationField(self.interfaceCopy.text("log_detail.port"), "port", draft.port)
    var rows: [[NSView]] = [
      [serverView],
      [
        port,
        ["ws", "httpupgrade"].contains(connection.transport)
          ? configurationField(
            self.interfaceCopy.text("interface_detail.01d9ab0ecc"), "host", draft.host) : NSView(),
      ],
    ]
    var spanning: Set<Int> = [0]
    if connection.security != "none" {
      spanning.insert(rows.count)
      rows.append([
        configurationField(self.interfaceCopy.text("interface_detail.87280a55d5"), "sni", draft.sni)
      ])
    }
    if connection.security == "tls" {
      let fragment = NSButton(
        checkboxWithTitle: self.interfaceCopy.text("action.routes.tls_fragment"), target: self,
        action: #selector(recordFragmentChanged(_:)))
      fragment.state = draft.recordFragment ? .on : .off
      fragment.font = PaxTheme.Typography.caption()
      fragment.toolTip = self.interfaceCopy.text("interface_detail.43c3caed45")
      fragment.setAccessibilityIdentifier("route-tls-record-fragment")
      spanning.insert(rows.count)
      rows.append([fragment])
    }
    let labels = NSStackView(views: [
      PaxFeatureTag(node.protocolName.uppercased()),
      PaxFeatureTag(connection.transport.uppercased(), style: .protocolLabel),
      PaxFeatureTag(connection.security.uppercased(), style: .protocolLabel),
    ])
    section(
      self.interfaceCopy.text("interface_detail.8d07216266"), symbol: "slider.horizontal.3",
      views: [
        PaxFormGrid(rows: rows, spanning: spanning),
        self.interfaceCopy.routeLabel(
          "action.routes.server_note", font: PaxTheme.Typography.caption(),
          secondary: true, wrapping: true), labels,
      ])
  }
  private func configurationField(_ title: String, _ key: String, _ value: String) -> NSView {
    let pair = field(title, value: value, identifier: key)
    pair.1.delegate = self
    inputs[key] = pair.1
    return pair.0
  }
}
