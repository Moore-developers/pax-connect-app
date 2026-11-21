import AppKit
import PaxCore
import PaxUI

/// Shared fields for creating and editing purchased proxies; no I/O or persistence.
@MainActor
final class RouteProxyForm: NSStackView {
  let context: PresentationContext

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
  private lazy var reveal = NSButton(
    checkboxWithTitle: context.text("network_controls.ffe21a6711"), target: nil, action: nil)
  private lazy var disclosure = NSButton(
    checkboxWithTitle: context.text("network_controls.44455611b9"), target: nil, action: nil)

  init(_ draft: ProxyRouteDraft, editing: Bool = false, context: PresentationContext = .chinese) {
    self.context = context

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
    authentication.addItems(withTitles: [
      context.text("network_controls.c0e75bfc70"), context.text("network_controls.4962150cec"),
    ])
    authentication.target = self
    authentication.action = #selector(authChanged)
    authentication.setAccessibilityIdentifier("proxy-auth")
    addArrangedSubview(row(context.text("network_controls.ba1de0bdf2"), kind))
    addArrangedSubview(
      PaxFormGrid(rows: [
        [
          row(context.text("network_controls.d3716cc5a2"), server),
          row(context.text("network_controls.e71ac32b54"), port),
        ]
      ]))
    addArrangedSubview(row(context.text("network_controls.d1ff8f98bc"), authentication))
    credentials.orientation = .vertical
    credentials.alignment = .leading
    credentials.spacing = PaxTheme.Spacing.regular
    credentials.addArrangedSubview(row(context.text("network_controls.1a3f0617d6"), username))
    let passwordFields = NSStackView(views: [password, visiblePassword])
    passwordFields.orientation = .vertical
    passwordFields.alignment = .leading
    passwordFields.spacing = 0
    credentials.addArrangedSubview(row(context.text("network_controls.a621ab606d"), passwordFields))
    credentials.addArrangedSubview(reveal)
    reveal.target = self
    reveal.action = #selector(revealChanged)
    addArrangedSubview(credentials)
    addArrangedSubview(row(context.text("network_controls.c6f3c5b91e"), name))
    advanced.orientation = .vertical
    advanced.alignment = .leading
    advanced.spacing = PaxTheme.Spacing.tight
    disclosure.target = self
    disclosure.action = #selector(advancedChanged)
    advanced.addArrangedSubview(disclosure)
    let serverNameFields = row(context.text("network_controls.dc92600e29"), sni)
    sniRow.addArrangedSubview(serverNameFields)
    advanced.addArrangedSubview(sniRow)
    sniRow.widthAnchor.constraint(equalTo: advanced.widthAnchor).isActive = true
    serverNameFields.widthAnchor.constraint(equalTo: sniRow.widthAnchor).isActive = true
    addArrangedSubview(advanced)
    addArrangedSubview(help)
    for (field, id, title) in [
      (server, "proxy-server", context.text("network_controls.d3716cc5a2")),
      (port, "proxy-port", context.text("network_controls.e71ac32b54")),
      (username, "proxy-username", context.text("network_controls.1a3f0617d6")),
      (password, "proxy-password", context.text("network_controls.a621ab606d")),
      (visiblePassword, "proxy-password-visible", context.text("network_controls.a621ab606d")),
      (name, "proxy-name", context.text("network_controls.2035432e8a")),
      (sni, "proxy-sni", context.text("network_controls.ec79bfcf63")),
    ] {
      field.font = PaxTheme.Typography.label()
      field.setAccessibilityIdentifier(id)
      field.setAccessibilityLabel(title)
    }
    password.heightAnchor.constraint(equalToConstant: PaxTheme.Routes.fieldHeight).isActive = true
    password.backgroundColor = PaxTheme.Routes.canvas
    server.placeholderString = context.text("network_controls.c0325588e5")
    port.placeholderString = context.text("network_controls.2546bd3979")
    name.placeholderString = context.text("network_controls.47d9506855")
    sni.placeholderString = context.text("network_controls.d4d46fe96e")
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
      ? context.text("network_controls.7c18e15a3e")
      : value.kind == .https
        ? context.text("network_controls.cefed18ab3")
        : value.kind == .http
          ? context.text("network_controls.f07c72ff31")
          : context.text("network_controls.6bd85fff8a")
    if !value.authenticated { help.stringValue += context.text("network_controls.f9b28d52de") }
    needsLayout = true
    onResize?()
  }
}
