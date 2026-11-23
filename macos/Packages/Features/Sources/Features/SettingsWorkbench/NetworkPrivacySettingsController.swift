import AppKit
import PaxCore
import PaxUI

/// Saved network intent is distinct from the running instance's frozen policy.
@MainActor
final class NetworkPrivacySettingsController: NSViewController, InterfaceLanguageConsumer {
  let interfaceCopy = InterfaceCopyBindings()
  private var statusMessage: (String, [String: String])? = ("settings.loading", [:])
  private let routes: RoutesStore
  private let mode = PaxRoutePicker(style: .toolbar)
  private lazy var webRTC = PaxToggle(
    label: "WebRTC 公网出口限制", showsLabel: false, target: self, action: #selector(changed))
  private var group: PaxPreferenceGroup?
  private var saved: NetworkPrivacy?
  private var task: Task<Void, Never>?
  private lazy var save = PaxRouteButton(
    "保存网络保护", style: .primary, target: self, action: #selector(commit))
  private lazy var retry = PaxRouteButton(
    "重试", style: .quiet, target: self, action: #selector(read))

  init(routes: RoutesStore) {
    self.routes = routes
    super.init(nibName: nil, bundle: nil)
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }

  override func loadView() {
    interfaceCopy.picker(mode, keys: ["settings.privacy.balanced", "settings.privacy.strict"])
    mode.widthAnchor.constraint(equalToConstant: PaxTheme.Settings.pickerWidth).isActive = true
    mode.setAccessibilityIdentifier("privacy-dns-mode")
    interfaceCopy.bind(mode, key: "settings.privacy.dns") { $0.setAccessibilityLabel($1) }
    interfaceCopy.bind(webRTC, key: "settings.privacy.webrtc") { $0.apply(label: $1) }
    interfaceCopy.button(save, key: "settings.privacy.save")
    interfaceCopy.button(retry, key: "settings.retry")
    interfaceCopy.onChange = { [weak self] in self?.renderStatus() }
    webRTC.setAccessibilityIdentifier("privacy-webrtc")
    save.setAccessibilityIdentifier("privacy-save")
    retry.setAccessibilityIdentifier("privacy-retry")
    mode.target = self
    mode.action = #selector(changed)
    let actions = NSStackView(views: [NSView(), retry, save])
    actions.spacing = PaxTheme.Spacing.tight
    let group = PaxPreferenceGroup(
      title: "", symbol: "lock.shield",
      rows: [
        interfaceCopy.preference(
          title: "settings.privacy.dns", detail: "settings.privacy.dns_detail", control: mode),
        interfaceCopy.preference(
          title: "settings.privacy.webrtc", detail: "settings.privacy.webrtc_detail",
          control: webRTC),
      ], style: .compact, footer: actions)
    self.group = group
    view = interfaceCopy.group(group, key: "settings.privacy.title")
    read()
  }

  private func showStatus(_ key: String, parameters: [String: String] = [:]) {
    statusMessage = key.isEmpty ? nil : (key, parameters)
    renderStatus()
  }
  private func renderStatus() {
    let note = interfaceCopy.text("settings.privacy.note")
    let current = statusMessage.map { interfaceCopy.text($0.0, $0.1) }
    group?.apply(summary: [current, note].compactMap { $0 }.joined(separator: " · "))
  }

  @objc private func read() {
    guard task == nil else { return }
    saved = nil
    save.isEnabled = false
    mode.isEnabled = false
    webRTC.isEnabled = false
    retry.isHidden = true
    guard routes.capabilities.contains("network.privacy.v1") else {
      showStatus("settings.backend_unavailable")
      retry.isHidden = false
      return
    }
    showStatus("settings.loading")
    task = Task { [weak self] in
      guard let self else { return }
      defer { task = nil }
      do {
        guard case .networkPrivacy(let value) = try await routes.measurement(.networkPrivacy) else {
          return
        }
        saved = value
        mode.selectItem(at: value.dnsMode == "strict" ? 1 : 0)
        webRTC.isChecked = value.webRTC
        mode.isEnabled = true
        webRTC.isEnabled = true
        showStatus("")
      } catch {
        showStatus(
          "settings.read_failed",
          parameters: [
            "detail": ProductErrorPresentation.display(error, context: interfaceCopy.context)
          ])
        retry.isHidden = false
      }
    }
  }

  @objc private func changed() {
    guard let saved else { return }
    save.isEnabled =
      (mode.indexOfSelectedItem == 1) != (saved.dnsMode == "strict")
      || webRTC.isChecked != saved.webRTC
    showStatus(save.isEnabled ? "settings.unsaved" : "")
  }

  @objc private func commit() {
    guard task == nil, var draft = saved else { return }
    draft.dnsMode = mode.indexOfSelectedItem == 1 ? "strict" : "balanced"
    draft.webRTC = webRTC.isChecked
    save.isEnabled = false
    mode.isEnabled = false
    webRTC.isEnabled = false
    showStatus("settings.saving")
    task = Task { [weak self] in
      guard let self else { return }
      defer {
        task = nil
        mode.isEnabled = true
        webRTC.isEnabled = true
      }
      do {
        guard
          case .networkPrivacy(let value) = try await routes.measurement(.saveNetworkPrivacy(draft))
        else { return }
        saved = value
        showStatus("settings.privacy.saved")
      } catch {
        showStatus(
          "settings.save_failed",
          parameters: [
            "detail": ProductErrorPresentation.display(error, context: interfaceCopy.context)
          ])
        save.isEnabled = true
      }
    }
  }
}
