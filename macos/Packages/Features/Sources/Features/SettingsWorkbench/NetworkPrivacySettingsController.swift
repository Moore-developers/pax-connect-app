import AppKit
import PaxCore
import PaxUI

/// Saved network intent is distinct from the running instance's frozen policy.
@MainActor
final class NetworkPrivacySettingsController: NSViewController {
  private let routes: RoutesStore
  private let mode = PaxRoutePicker(style: .toolbar)
  private let webRTC = PaxRoutePicker(style: .toolbar)
  private let status = PaxText.label(
    "正在读取网络保护设置…", font: PaxTheme.Typography.caption(), wrapping: true)
  private var saved: NetworkPrivacy?
  private var task: Task<Void, Never>?
  private lazy var save = PaxRouteButton(
    "保存网络保护", style: .primary, target: self, action: #selector(commit))
  private lazy var reload = PaxRouteButton(
    "重新读取", style: .secondary, target: self, action: #selector(retry))
  @objc private func retry() { read() }
  init(routes: RoutesStore) {
    self.routes = routes
    super.init(nibName: nil, bundle: nil)
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    mode.addItems(withTitles: ["加密分流（推荐）", "严格 DNS 隐私"])
    webRTC.addItems(withTitles: ["保留正常功能", "增强公网出口限制"])
    mode.setAccessibilityIdentifier("privacy-dns-mode")
    webRTC.setAccessibilityIdentifier("privacy-webrtc")
    for picker in [mode, webRTC] {
      picker.target = self
      picker.action = #selector(changed)
    }
    view = PaxPreferenceGroup(
      title: "DNS 与 WebRTC", symbol: "lock.shield",
      rows: [
        PaxPreferenceRow(
          title: "DNS 保护", detail: "国内加密直连，海外经代理加密；严格模式让公网业务解析统一经代理。节点引导及明确内网域名例外。", control: mode),
        PaxPreferenceRow(
          title: "WebRTC 公网出口限制", detail: "增强限制让识别的公网 STUN 经代理，UDP 不可用时失败而不直连，可能影响通话；浏览器插件仍需保留。",
          control: webRTC),
        PaxText.label(
          "阿里／腾讯 DoH 国内主备，Cloudflare／Google DoH 海外主备；不静默回退明文。仅代理/TUN启用时提供保护，DNS策略不等于所有业务匿名。",
          font: PaxTheme.Typography.caption(), secondary: true, wrapping: true),
        status, reload, save,
      ])
    mode.isEnabled = false
    webRTC.isEnabled = false
    save.isEnabled = false
    read()
  }
  private func read() {
    task?.cancel()
    saved = nil
    save.isEnabled = false
    mode.isEnabled = false
    webRTC.isEnabled = false
    guard routes.capabilities.contains("network.privacy.v1") else {
      status.stringValue = "后台尚未就绪或不支持网络保护设置，请连接新版后台后重新读取。"
      return
    }
    reload.isEnabled = false
    task = Task { [weak self] in
      guard let self else { return }
      defer { reload.isEnabled = true }
      do {
        guard case .networkPrivacy(let value) = try await routes.measurement(.networkPrivacy),
          !Task.isCancelled
        else { return }
        saved = value
        mode.selectItem(at: value.dnsMode == "strict" ? 1 : 0)
        webRTC.selectItem(at: value.webRTC ? 1 : 0)
        mode.isEnabled = true
        webRTC.isEnabled = true
        status.stringValue = "已保存；修改后下次开启代理生效。尚未进行泄露检测。"
      } catch { status.stringValue = "读取失败：" + ProductErrorPresentation.display(error) }
    }
  }
  @objc private func changed() { save.isEnabled = saved != nil }
  @objc private func commit() {
    guard var draft = saved else { return }
    draft.dnsMode = mode.indexOfSelectedItem == 1 ? "strict" : "balanced"
    draft.webRTC = webRTC.indexOfSelectedItem == 1
    save.isEnabled = false
    reload.isEnabled = false
    mode.isEnabled = false
    webRTC.isEnabled = false
    task = Task { [weak self] in
      guard let self else { return }
      defer {
        reload.isEnabled = true
        mode.isEnabled = true
        webRTC.isEnabled = true
      }
      do {
        guard
          case .networkPrivacy(let value) = try await routes.measurement(.saveNetworkPrivacy(draft))
        else { return }
        saved = value
        status.stringValue = "已保存，下次开启代理生效；当前连接策略保持。"
      } catch {
        status.stringValue = "保存失败，草稿保留：" + ProductErrorPresentation.display(error)
        save.isEnabled = true
      }
    }
  }
}
