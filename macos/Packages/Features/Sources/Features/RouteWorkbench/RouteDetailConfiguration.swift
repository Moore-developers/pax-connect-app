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
          try ProxyRouteDraft(document: draft.json, name: draft.name), editing: true)
        form.onResize = { [weak self] in self?.pane.invalidateContentSize() }
        proxyForm = form
        section("代理设置", symbol: "slider.horizontal.3", views: [form])
      } catch { showError(error) }
      return
    }
    section(
      "线路信息", symbol: "info.circle",
      views: [configurationField("线路名称", "name", draft.name)])
    if let chain = store.info(id).chain {
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
        "连接路径", symbol: "point.3.connected.trianglepath.dotted",
        views: [
          PaxSectionHeader("前置线路"), popup,
          PaxRouteText.label("最终线路：" + (store.node(chain.final)?.name ?? "不可用")),
          PaxConnectionPath(nodes: store.path(id, frontID: draft.frontID)),
        ])
      return
    }
    let server = field("服务器地址", value: draft.server, identifier: "server")
    inputs["server"] = server.1
    server.1.delegate = self
    var serverView: NSView = server.0
    if connection.supportsCF {
      let choose = button("选择 CF IP…", #selector(chooseCF))
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
        checkboxWithTitle: "作为 CF 入口", target: self, action: #selector(cfEnabled(_:)))
      enabled.state = draft.cf ? .on : .off
      enabled.font = PaxTheme.Typography.caption()
      vertical.addArrangedSubview(enabled)
      serverView = vertical
    }
    let port = configurationField("端口", "port", draft.port)
    var rows: [[NSView]] = [
      [serverView],
      [
        port,
        ["ws", "httpupgrade"].contains(connection.transport)
          ? configurationField("请求域名 · Host", "host", draft.host) : NSView(),
      ],
    ]
    var spanning: Set<Int> = [0]
    if connection.security != "none" {
      spanning.insert(rows.count)
      rows.append([configurationField("服务器名称 · SNI", "sni", draft.sni)])
    }
    if connection.security == "tls" {
      let fragment = NSButton(
        checkboxWithTitle: "TLS 记录分片", target: self, action: #selector(recordFragmentChanged(_:)))
      fragment.state = draft.recordFragment ? .on : .off
      fragment.font = PaxTheme.Typography.caption()
      fragment.toolTip = "拆分 TLS 握手记录以改善部分网络的连接兼容性，保留证书校验。"
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
      "连接设置", symbol: "slider.horizontal.3",
      views: [
        PaxFormGrid(rows: rows, spanning: spanning),
        PaxRouteText.label(
          "服务器地址决定连接位置；Host 与 SNI 保留服务端要求的域名。", font: PaxTheme.Typography.caption(),
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
