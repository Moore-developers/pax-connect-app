import AppKit
import Observation
import PaxCore
import PaxUI

/// Each client registration is independent of entry readiness and backend availability.
@MainActor
final class MCPAccessSettingsController: NSViewController, InterfaceLanguageConsumer {
  let interfaceCopy = InterfaceCopyBindings()
  private var feedback: (key: String, clientID: String?)?
  private var group: PaxPreferenceGroup?
  private var entryRow: PaxPreferenceRow?
  private var checkButton: PaxRouteButton?
  private var entryControlsWidth: NSLayoutConstraint?
  private var verifyButton: PaxRouteButton?
  private var clientRows: [(MCPAccessReport.Client, PaxPreferenceRow, PaxRouteButton)] = []
  private let actions: MCPAccessActions
  private let connection: ConnectionStore
  private let content = NSStackView()
  private let permission = PaxRoutePicker(style: .toolbar)
  private var report: MCPAccessReport?
  private var busy = false

  init(actions: MCPAccessActions, connection: ConnectionStore) {
    self.actions = actions
    self.connection = connection
    super.init(nibName: nil, bundle: nil)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  override func loadView() {
    view = content
    content.orientation = .vertical
    content.alignment = .leading
    content.spacing = PaxTheme.Spacing.tight
    interfaceCopy.picker(permission, keys: ["settings.mcp.read", "settings.mcp.diagnose"])
    permission.setAccessibilityIdentifier("mcp-initial-permissions")
    interfaceCopy.bind(permission, key: "settings.mcp.permissions_ax") {
      $0.setAccessibilityLabel($1)
    }
    interfaceCopy.onChange = { [weak self] in self?.updateCopy() }
    permission.widthAnchor.constraint(equalToConstant: PaxTheme.Settings.pickerWidth).isActive =
      true
    render()
    observeBackend()
  }

  override func viewWillAppear() {
    super.viewWillAppear()
    run("inspect")
  }

  private func observeBackend() {
    withObservationTracking {
      renderBackend()
    } onChange: { [weak self] in
      Task { @MainActor in self?.observeBackend() }
    }
  }

  private func render() {
    for item in content.arrangedSubviews {
      content.removeArrangedSubview(item)
      item.removeFromSuperview()
    }
    clientRows.removeAll()
    let check = interfaceCopy.button(
      PaxRouteButton("", target: self, action: #selector(inspect)), key: "settings.mcp.inspect")
    checkButton = check
    check.setAccessibilityIdentifier("mcp-inspect")
    check.isEnabled = !busy
    let verify = PaxRouteButton(
      interfaceCopy.text(report?.entry == "repair" ? "settings.mcp.repair" : "settings.mcp.verify"),
      target: self, action: #selector(verifyEntry))
    verifyButton = verify
    verify.setAccessibilityIdentifier("mcp-verify-entry")
    verify.isEnabled = !busy && report?.entry != "missing" && report != nil
    let controls = NSStackView(views: [check, verify])
    controls.spacing = PaxTheme.Spacing.tight
    controls.orientation = .horizontal
    controls.alignment = .centerY
    let width = controls.widthAnchor.constraint(equalToConstant: 0)
    width.isActive = true
    entryControlsWidth = width
    updateEntryControlsWidth()
    let entry = PaxPreferenceRow(
      title: "Pax MCP", detail: "", control: controls, style: .compact)
    entryRow = entry
    var rows: [NSView] = [entry]
    for client in report?.clients ?? [] {
      let button = PaxRouteButton(
        buttonTitle(client), target: self, action: #selector(connectClient(_:)))
      button.identifier = NSUserInterfaceItemIdentifier(client.id)
      button.setAccessibilityIdentifier("mcp-connect-" + client.id)
      button.isEnabled = !busy && ["missing", "outdated", "disabled"].contains(client.state)
      let row = PaxPreferenceRow(
        title: clientName(client), detail: clientDescription(client), control: button,
        style: .compact)
      clientRows.append((client, row, button))
      rows.append(row)
    }
    let permissions = PaxPreferenceRow(title: "", detail: "", control: permission, style: .compact)
    interfaceCopy.bind(permissions) { row, context in
      row.apply(
        title: context.text("settings.mcp.initial_permissions"),
        detail: context.text("settings.mcp.permissions_detail") + " "
          + context.text("settings.mcp.note"))
    }
    rows.append(permissions)
    permission.isEnabled = !busy
    let group = PaxPreferenceGroup(title: "", symbol: "terminal", rows: rows, style: .compact)
    self.group = group
    interfaceCopy.group(group, key: "settings.mcp.title")
    content.addArrangedSubview(group)
    group.widthAnchor.constraint(equalTo: content.widthAnchor).isActive = true
    renderBackend()
    renderFeedback()
  }

  private var entryDescription: String {
    guard let report else { return interfaceCopy.text("settings.mcp.inspecting") }
    if report.verified { return interfaceCopy.text("settings.mcp.verified") }
    switch report.entry {
    case "ready": return interfaceCopy.text("settings.mcp.ready")
    case "missing": return interfaceCopy.text("settings.mcp.missing")
    default: return interfaceCopy.text("settings.mcp.needs_repair")
    }
  }

  private func clientName(_ client: MCPAccessReport.Client) -> String {
    client.id == "dsh-desktop" ? interfaceCopy.text("settings.mcp.dsh_desktop") : client.name
  }
  private func buttonTitle(_ client: MCPAccessReport.Client) -> String {
    switch client.state {
    case "configured": return interfaceCopy.text("settings.mcp.configured")
    case "outdated": return interfaceCopy.text("settings.mcp.update")
    case "disabled": return interfaceCopy.text("settings.mcp.enable")
    case "missing": return interfaceCopy.text("settings.mcp.connect")
    default: return interfaceCopy.text("settings.mcp.unavailable")
    }
  }

  private func clientDescription(_ client: MCPAccessReport.Client) -> String {
    let scope = client.id == "codex" ? interfaceCopy.text("settings.mcp.codex_scope") : ""
    switch client.state {
    case "configured":
      return interfaceCopy.text(
        "settings.mcp.client_configured",
        ["scope": scope, "entry": client.entryName, "permissions": client.permissions])
    case "missing": return interfaceCopy.text("settings.mcp.client_missing", ["scope": scope])
    case "outdated": return interfaceCopy.text("settings.mcp.client_outdated", ["scope": scope])
    case "disabled": return interfaceCopy.text("settings.mcp.client_disabled", ["scope": scope])
    case "inherited": return interfaceCopy.text("settings.mcp.client_inherited")
    case "not_found": return interfaceCopy.text("settings.mcp.client_not_found")
    case "unreadable": return interfaceCopy.text("settings.mcp.client_unreadable")
    case "invalid": return interfaceCopy.text("settings.mcp.client_invalid")
    default: return interfaceCopy.text("settings.mcp.client_conflict")
    }
  }

  private func setFeedback(_ key: String?, client: MCPAccessReport.Client?) {
    feedback = key.map { ($0, client?.id) }
    renderFeedback()
  }
  private func renderBackend() {
    let backend = interfaceCopy.text(
      connection.connection == .connected
        ? "settings.mcp.backend_ready" : "settings.mcp.backend_missing")
    group?.apply(summary: backend)
  }
  private func updateCopy() {
    renderBackend()
    verifyButton?.title = interfaceCopy.text(
      report?.entry == "repair" ? "settings.mcp.repair" : "settings.mcp.verify")
    updateEntryControlsWidth()
    for (client, row, button) in clientRows {
      row.apply(title: clientName(client), detail: clientDescription(client))
      button.title = buttonTitle(client)
    }
    renderFeedback()
  }
  private func renderFeedback() {
    let detail =
      feedback.map {
        $0.key == "error" ? message(report?.error ?? "") : interfaceCopy.text($0.key)
      } ?? ""
    let target = clientRows.first { $0.0.id == feedback?.clientID }?.1
    let entry =
      target == nil && !detail.isEmpty ? entryDescription + " · " + detail : entryDescription
    entryRow?.apply(feedback: entry)
    for (_, row, _) in clientRows {
      row.apply(feedback: row === target ? detail : "")
    }
  }
  private func updateEntryControlsWidth() {
    guard let checkButton, let verifyButton else { return }
    entryControlsWidth?.constant =
      checkButton.intrinsicContentSize.width
      + verifyButton.intrinsicContentSize.width + PaxTheme.Spacing.tight
  }
  @objc private func inspect() { run("inspect") }
  @objc private func verifyEntry() { run(report?.entry == "repair" ? "repair" : "verify") }
  @objc private func connectClient(_ sender: NSButton) {
    guard let id = sender.identifier?.rawValue,
      let client = report?.clients.first(where: { $0.id == id })
    else { return }
    run("connect", client: client)
  }

  private func run(_ action: String, client: MCPAccessReport.Client? = nil) {
    guard !busy else { return }
    busy = true
    let allow = permission.indexOfSelectedItem == 1 ? "read,diagnose" : "read"
    setFeedback(action == "inspect" ? nil : "settings.mcp.working", client: client)
    render()
    Task { [weak self] in
      guard let self else { return }
      defer {
        busy = false
        render()
      }
      do {
        report = try await actions.run(action, client?.id ?? "", allow, client?.state ?? "")
        guard let report else { return }
        if !report.error.isEmpty {
          setFeedback("error", client: client)
        } else if action == "connect" {
          setFeedback("settings.mcp.connected_feedback", client: client)
        } else if action == "verify" {
          setFeedback("settings.mcp.verified_feedback", client: client)
        } else if action == "repair" {
          setFeedback("settings.mcp.repaired_feedback", client: client)
        }
      } catch {
        setFeedback("settings.mcp.check_failed", client: client)
      }
    }
  }

  private func message(_ code: String) -> String {
    switch code {
    case "configuration_changed": return interfaceCopy.text("settings.mcp.configuration_changed")
    case "configuration_conflict": return interfaceCopy.text("settings.mcp.configuration_conflict")
    case "configuration_backup_failed":
      return interfaceCopy.text("settings.mcp.configuration_backup_failed")
    case "configuration_commit_uncertain":
      return interfaceCopy.text("settings.mcp.configuration_commit_uncertain")
    case "entry_probe_failed": return interfaceCopy.text("settings.mcp.entry_probe_failed")
    case "installation_invalid": return interfaceCopy.text("settings.mcp.installation_invalid")
    default: return interfaceCopy.text("settings.mcp.unknown_error")
    }
  }
}
