import AppKit
import PaxCore
import PaxUI

/// Saved network intent is distinct from the running instance's frozen policy.
@MainActor
final class NetworkPrivacySettingsController: NSViewController {
  private let routes: RoutesStore
  private let mode = PaxRoutePicker(style: .toolbar)
  private lazy var webRTC = PaxToggle(
    label: "WebRTC 公网出口限制", showsLabel: false, target: self, action: #selector(changed))
  private let status = PaxText.label(
    "正在读取…", font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
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
    mode.addItems(withTitles: ["加密分流", "严格隐私"])
    mode.widthAnchor.constraint(equalToConstant: PaxTheme.Settings.pickerWidth).isActive = true
    mode.setAccessibilityIdentifier("privacy-dns-mode")
    mode.setAccessibilityLabel("DNS 保护")
    webRTC.setAccessibilityIdentifier("privacy-webrtc")
    save.setAccessibilityIdentifier("privacy-save")
    retry.setAccessibilityIdentifier("privacy-retry")
    mode.target = self
    mode.action = #selector(changed)
    let note = PaxText.label(
      "仅开启代理时提供保护；修改后，下次开启代理生效。",
      font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
    note.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    let actions = NSStackView(views: [note, NSView(), retry, save])
    actions.spacing = PaxTheme.Spacing.tight
    let footer = NSStackView(views: [actions, status])
    footer.orientation = .vertical
    footer.alignment = .leading
    footer.spacing = PaxTheme.Spacing.tight
    for item in footer.arrangedSubviews {
      item.widthAnchor.constraint(equalTo: footer.widthAnchor).isActive = true
    }
    view = PaxPreferenceGroup(
      title: "网络保护", symbol: "lock.shield",
      rows: [
        PaxPreferenceRow(
          title: "DNS 保护", detail: "加密分流按规则选择直连或代理；严格隐私经代理解析公网域名。节点引导与内网域名例外。", control: mode,
          style: .compact),
        PaxPreferenceRow(
          title: "WebRTC 公网出口限制", detail: "识别的公网 STUN 经代理，可能影响通话；不保证隐藏所有 IP。", control: webRTC,
          style: .compact),
      ], style: .compact, footer: footer)
    read()
  }

  private func showStatus(_ message: String) {
    status.stringValue = message
    status.isHidden = message.isEmpty
  }

  @objc private func read() {
    guard task == nil else { return }
    saved = nil
    save.isEnabled = false
    mode.isEnabled = false
    webRTC.isEnabled = false
    retry.isHidden = true
    guard routes.capabilities.contains("network.privacy.v1") else {
      showStatus("后台尚未就绪，连接后可重试。")
      retry.isHidden = false
      return
    }
    showStatus("正在读取…")
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
        showStatus("读取失败：" + ProductErrorPresentation.display(error))
        retry.isHidden = false
      }
    }
  }

  @objc private func changed() {
    guard let saved else { return }
    save.isEnabled =
      (mode.indexOfSelectedItem == 1) != (saved.dnsMode == "strict")
      || webRTC.isChecked != saved.webRTC
    showStatus(save.isEnabled ? "尚未保存" : "")
  }

  @objc private func commit() {
    guard task == nil, var draft = saved else { return }
    draft.dnsMode = mode.indexOfSelectedItem == 1 ? "strict" : "balanced"
    draft.webRTC = webRTC.isChecked
    save.isEnabled = false
    mode.isEnabled = false
    webRTC.isEnabled = false
    showStatus("正在保存…")
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
        showStatus("已保存，下次开启代理生效")
      } catch {
        showStatus("保存失败，草稿保留：" + ProductErrorPresentation.display(error))
        save.isEnabled = true
      }
    }
  }
}
