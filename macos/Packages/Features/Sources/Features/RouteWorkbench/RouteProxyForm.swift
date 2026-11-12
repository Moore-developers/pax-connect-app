import AppKit
import PaxCore
import PaxUI

/// Shared fields for creating and editing purchased proxies; no I/O or persistence.
@MainActor
final class RouteProxyForm: NSStackView {
  var onResize: (() -> Void)?
  private var draft: ProxyRouteDraft
  private let kind = PaxRoutePicker()
  private let authentication = PaxRoutePicker()
  private let server = PaxRouteField("")
  private let port = PaxRouteField("")
  private let username = PaxRouteField("")
  private let password = NSSecureTextField()
  private let visiblePassword = PaxRouteField("")
  private let name = PaxRouteField("")
  private let sni = PaxRouteField("")
  private let credentials = NSStackView()
  private let advanced = NSStackView()
  private let sniRow = NSStackView()
  private let help = PaxRouteText.label("", secondary: true, wrapping: true)
  private let reveal = NSButton(checkboxWithTitle: "显示密码", target: nil, action: nil)
  private let disclosure = NSButton(checkboxWithTitle: "高级设置", target: nil, action: nil)

  init(_ draft: ProxyRouteDraft, editing: Bool = false) {
    self.draft = draft
    super.init(frame: .zero)
    orientation = .vertical
    alignment = .leading
    spacing = PaxTheme.Spacing.regular
    kind.addItems(withTitles: ProxyRouteDraft.Kind.allCases.map(\.rawValue))
    kind.target = self
    kind.action = #selector(kindChanged)
    kind.isEnabled = !editing
    kind.setAccessibilityIdentifier("proxy-kind")
    authentication.addItems(withTitles: ["用户名和密码", "无需账号密码"])
    authentication.target = self
    authentication.action = #selector(authChanged)
    authentication.setAccessibilityIdentifier("proxy-auth")
    addArrangedSubview(row("代理类型", kind))
    addArrangedSubview(PaxFormGrid(rows: [[row("服务器地址", server), row("端口", port)]]))
    addArrangedSubview(row("认证方式", authentication))
    credentials.orientation = .vertical
    credentials.alignment = .leading
    credentials.spacing = PaxTheme.Spacing.regular
    credentials.addArrangedSubview(row("用户名", username))
    let passwordFields = NSStackView(views: [password, visiblePassword])
    passwordFields.orientation = .vertical
    passwordFields.alignment = .leading
    passwordFields.spacing = 0
    credentials.addArrangedSubview(row("密码", passwordFields))
    credentials.addArrangedSubview(reveal)
    reveal.target = self
    reveal.action = #selector(revealChanged)
    addArrangedSubview(credentials)
    addArrangedSubview(row("线路名称（可选）", name))
    advanced.orientation = .vertical
    advanced.alignment = .leading
    advanced.spacing = PaxTheme.Spacing.tight
    disclosure.target = self
    disclosure.action = #selector(advancedChanged)
    advanced.addArrangedSubview(disclosure)
    let serverNameFields = row("服务器名称（SNI）", sni)
    sniRow.addArrangedSubview(serverNameFields)
    advanced.addArrangedSubview(sniRow)
    sniRow.widthAnchor.constraint(equalTo: advanced.widthAnchor).isActive = true
    serverNameFields.widthAnchor.constraint(equalTo: sniRow.widthAnchor).isActive = true
    addArrangedSubview(advanced)
    addArrangedSubview(help)
    for (field, id, title) in [
      (server, "proxy-server", "服务器地址"), (port, "proxy-port", "端口"),
      (username, "proxy-username", "用户名"), (password, "proxy-password", "密码"),
      (visiblePassword, "proxy-password-visible", "密码"), (name, "proxy-name", "线路名称"),
      (sni, "proxy-sni", "服务器名称"),
    ] {
      field.font = PaxTheme.Typography.label()
      field.setAccessibilityIdentifier(id)
      field.setAccessibilityLabel(title)
    }
    password.heightAnchor.constraint(equalToConstant: PaxTheme.Routes.fieldHeight).isActive = true
    password.backgroundColor = PaxTheme.Routes.canvas
    server.placeholderString = "IP 或域名"
    port.placeholderString = "服务商提供的端口"
    name.placeholderString = "留空自动生成"
    sni.placeholderString = "默认跟随服务器域名"
    for child in arrangedSubviews {
      child.widthAnchor.constraint(equalTo: widthAnchor).isActive = true
    }
    for child in credentials.arrangedSubviews {
      child.widthAnchor.constraint(equalTo: credentials.widthAnchor).isActive = true
    }
    password.widthAnchor.constraint(equalTo: passwordFields.widthAnchor).isActive = true
    visiblePassword.widthAnchor.constraint(equalTo: passwordFields.widthAnchor).isActive = true
    apply(draft)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  private func row(_ title: String, _ control: NSView) -> NSStackView {
    let result = NSStackView(views: [
      PaxRouteText.label(title, font: PaxTheme.Typography.caption(), secondary: true), control,
    ])
    result.orientation = .vertical
    result.alignment = .leading
    result.spacing = PaxTheme.Spacing.tight
    control.widthAnchor.constraint(equalTo: result.widthAnchor).isActive = true
    return result
  }
  func apply(_ value: ProxyRouteDraft) {
    draft = value
    kind.selectItem(withTitle: value.kind.rawValue)
    authentication.selectItem(at: value.authenticated ? 0 : 1)
    server.stringValue = value.server
    port.stringValue = value.port
    username.stringValue = value.username
    password.stringValue = value.password
    visiblePassword.stringValue = value.password
    name.stringValue = value.name
    sni.stringValue = value.serverName
    reveal.state = .off
    disclosure.state = value.serverName.isEmpty ? .off : .on
    updateVisibility()
  }
  func value() -> ProxyRouteDraft {
    var value = draft
    value.kind = ProxyRouteDraft.Kind.allCases[max(0, kind.indexOfSelectedItem)]
    value.authenticated = authentication.indexOfSelectedItem == 0
    value.server = server.stringValue
    value.port = port.stringValue
    value.username = username.stringValue
    value.password = reveal.state == .on ? visiblePassword.stringValue : password.stringValue
    value.name = name.stringValue
    value.serverName = sni.stringValue
    return value
  }
  func setEnabled(_ enabled: Bool, editing: Bool = false) {
    for control: NSControl in [
      kind, authentication, server, port, username, password, visiblePassword, name, sni, reveal,
      disclosure,
    ] { control.isEnabled = enabled }
    kind.isEnabled = enabled && !editing
  }
  @objc private func kindChanged() {
    draft.needsProtocolConfirmation = false
    updateVisibility()
  }
  @objc private func authChanged() { updateVisibility() }
  @objc private func advancedChanged() { updateVisibility() }
  @objc private func revealChanged() {
    if reveal.state == .on {
      visiblePassword.stringValue = password.stringValue
    } else {
      password.stringValue = visiblePassword.stringValue
    }
    updateVisibility()
  }
  private func updateVisibility() {
    let value = value()
    credentials.isHidden = !value.authenticated
    password.isHidden = reveal.state == .on
    visiblePassword.isHidden = reveal.state != .on
    advanced.isHidden = value.kind != .https
    sniRow.isHidden = disclosure.state != .on
    help.stringValue =
      value.needsProtocolConfirmation
      ? "请确认代理类型：打开上方菜单选择 SOCKS5、HTTP 或 HTTPS。"
      : value.kind == .https
        ? "HTTPS 加密连接代理服务器，并校验证书。能访问 HTTPS 网站不代表必须选择 HTTPS 代理。"
        : value.kind == .http
          ? "HTTP 可访问 HTTPS 网站；代理连接本身未加密。普通 HTTP 代理不支持 UDP。"
          : "SOCKS5 本身不加密；UDP 是否可用取决于服务商。"
    if !value.authenticated { help.stringValue += " IP 白名单请在服务商后台设置。" }
    needsLayout = true
    onResize?()
  }
}
