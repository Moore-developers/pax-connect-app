import AppKit
import Observation
import PaxCore
import PaxUI

/// Production measurement settings are saved by the daemon, not the demo store.
@MainActor
final class MeasurementSettingsViewController: NSViewController {
  private let routes: RoutesStore
  private var baseline: MeasurementPreferences?
  private var task: Task<Void, Never>?
  private var assessmentID: String?
  private let latencyMode = PaxRoutePicker()
  private let exitMode = PaxRoutePicker()
  private let downloadMode = PaxRoutePicker()
  private let latency = PaxRouteField("32")
  private let exit = PaxRouteField("8")
  private let download = PaxRouteField("8")
  private let status = NSTextField(wrappingLabelWithString: "正在读取测量配置…")
  private let poolStatus = NSTextField(wrappingLabelWithString: "出口查询池状态待读取")
  private let recommendation = NSTextField(wrappingLabelWithString: "尚未评估当前网络")
  private var saveButton: PaxRouteButton?
  init(routes: RoutesStore) {
    self.routes = routes
    super.init(nibName: nil, bundle: nil)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    view = WorkbenchSurface()
    for mode in [latencyMode, downloadMode] {
      mode.addItems(withTitles: ["使用默认值", "手动配置", "自动推荐"])
      mode.widthAnchor.constraint(equalToConstant: PaxTheme.Settings.pickerWidth).isActive = true
    }
    exitMode.addItems(withTitles: ["自动调度（上限8）", "手动上限"])
    exitMode.widthAnchor.constraint(equalToConstant: PaxTheme.Settings.pickerWidth).isActive = true
    exitMode.target = self
    exitMode.action = #selector(exitModeChanged)
    let stack = NSStackView()
    stack.orientation = .vertical
    stack.alignment = .leading
    stack.spacing = PaxTheme.Spacing.medium
    let note = PaxText.label(
      "HTTPS 延迟分别记录首次连接与同会话三次请求的中位数。下载默认 8 路，每条最多 8 MiB／30 秒；延迟和下载配置下批生效；出口上限保存后影响待发请求。",
      font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
    stack.addArrangedSubview(note)
    let entries: [(String, String, NSPopUpButton, NSTextField)] = [
      ("HTTPS 延迟", "并发 1–100；默认按本机硬件确定。", latencyMode, latency),
      ("出口 IP", "HTTP请求上限1–32；备用与补地区也计数。", exitMode, exit),
      ("下载速度", "并发 1–8；默认 8，每批完成后自动继续。", downloadMode, download),
    ]
    let rows = entries.map { title, detail, mode, field in
      field.widthAnchor.constraint(equalToConstant: PaxTheme.Settings.numberWidth).isActive = true
      field.setAccessibilityLabel(title + "并发数")
      let controls = NSStackView(views: [mode, field])
      controls.spacing = PaxTheme.Spacing.tight
      controls.widthAnchor.constraint(
        equalToConstant: PaxTheme.Settings.pickerWidth + PaxTheme.Settings.numberWidth
          + PaxTheme.Spacing.tight
      ).isActive = true
      return PaxPreferenceRow(title: title, detail: detail, control: controls)
    }
    stack.addArrangedSubview(PaxPreferenceGroup(title: "测试并发", symbol: "speedometer", rows: rows))
    poolStatus.font = PaxTheme.Typography.caption()
    poolStatus.textColor = PaxTheme.Color.textSecondary
    poolStatus.setAccessibilityIdentifier("measurement-exit-pool-state")
    stack.addArrangedSubview(poolStatus)
    latency.setAccessibilityIdentifier("measurement-latency-parallel")
    exit.setAccessibilityIdentifier("measurement-exit-parallel")
    download.setAccessibilityIdentifier("measurement-download-parallel")
    let save = PaxRouteButton(
      "保存测量配置", style: .primary, target: self, action: #selector(savePreferences))
    save.setAccessibilityIdentifier("measurement-save")
    saveButton = save
    let reset = PaxRouteButton("恢复默认", target: self, action: #selector(resetPreferences))
    reset.setAccessibilityIdentifier("measurement-reset")
    let assess = PaxRouteButton("评估下载并发", target: self, action: #selector(evaluate))
    let stop = PaxRouteButton("停止评估", target: self, action: #selector(stopAssessment))
    let reload = PaxRouteButton("重新读取", target: self, action: #selector(reloadPreferences))
    reload.setAccessibilityIdentifier("measurement-reload")
    let assessment = NSStackView(views: [assess, stop])
    assessment.spacing = PaxTheme.Spacing.tight
    assessment.widthAnchor.constraint(equalToConstant: assessment.fittingSize.width).isActive = true
    stack.addArrangedSubview(
      PaxPreferenceGroup(
        title: "当前网络", symbol: "network",
        rows: [
          PaxPreferenceRow(
            title: "下载推荐并发", detail: "比较 2／4／8 路下载；最多8分钟，可停止；最多每条48 MiB。", control: assessment)
        ]))
    recommendation.font = PaxTheme.Typography.caption()
    recommendation.textColor = PaxTheme.Color.textSecondary
    status.font = PaxTheme.Typography.caption()
    status.textColor = PaxTheme.Color.textSecondary
    stack.addArrangedSubview(recommendation)
    let actions = NSStackView(views: [status, NSView(), reload, reset, save])
    actions.spacing = PaxTheme.Spacing.tight
    stack.addArrangedSubview(actions)
    for item in stack.arrangedSubviews {
      item.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
    }
    stack.translatesAutoresizingMaskIntoConstraints = false
    view.addSubview(stack)
    NSLayoutConstraint.activate([
      stack.leadingAnchor.constraint(equalTo: view.leadingAnchor),
      stack.trailingAnchor.constraint(equalTo: view.trailingAnchor),
      stack.topAnchor.constraint(equalTo: view.topAnchor),
      stack.bottomAnchor.constraint(equalTo: view.bottomAnchor, constant: -PaxTheme.Spacing.roomy),
    ])
    reloadPreferences()
    observe()
  }
  private func observe() {
    withObservationTracking {
      saveButton?.isEnabled = routes.connected && baseline != nil && task == nil
      _ = routes.downloadOperationID
      applyPoolState(routes.exitPoolState ?? baseline?.exitPoolState)
      if let assessmentID, let result = routes.measurementResult(assessmentID) {
        status.stringValue =
          result.ok
          ? "评估完成，重新读取配置查看推荐"
          : result.error.map(ProductErrorPresentation.message) ?? "评估失败"
        self.assessmentID = nil
      }
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observe() }
    }
  }
  @objc private func exitModeChanged() {
    let supported = routes.capabilities.contains("measurement.exit_pool.v1")
    exitMode.isEnabled = supported
    exit.isEnabled = supported && exitMode.indexOfSelectedItem == 1
    if supported && exitMode.indexOfSelectedItem == 0 { exit.stringValue = "8" }
  }
  private func applyPoolState(_ value: ExitPoolState?) {
    guard routes.capabilities.contains("measurement.exit_pool.v1"), let value else {
      poolStatus.stringValue = "后台尚未提供查询池状态，请更新后台后使用请求并发设置"
      return
    }
    poolStatus.stringValue =
      "请求上限 \(value.configuredLimit) · 执行 \(value.inFlight) · 等待 \(value.queued)（含重试 \(value.retrying)）· 服务可用 \(value.availableProviders)/\(value.totalProviders)"
  }
  @objc private func reloadPreferences() {
    guard task == nil else { return }
    task = Task { [weak self, routes] in
      do {
        if case .preferences(let value) = try await routes.measurement(.preferences) {
          self?.baseline = value
          self?.render(value)
          self?.status.stringValue = "配置已读取"
        }
      } catch { self?.status.stringValue = ProductErrorPresentation.display(error) }
      self?.task = nil
      self?.observe()
    }
  }
  private func render(_ value: MeasurementPreferences) {
    latency.stringValue = String(value.latency)
    exit.stringValue = String(value.exit)
    download.stringValue = String(value.download)
    for (picker, value) in [
      (latencyMode, value.latencyMode), (downloadMode, value.downloadMode),
    ] { picker.selectItem(at: value == "manual" ? 1 : value == "auto" ? 2 : 0) }
    exitMode.selectItem(at: value.exitMode == "manual" ? 1 : 0)
    if routes.capabilities.contains("measurement.exit_pool.v1"), value.exitMode != "manual" {
      exit.stringValue = "8"
    }
    exitModeChanged()
    applyPoolState(routes.exitPoolState ?? value.exitPoolState)
    recommendation.stringValue =
      value.recommendedDownload > 0
      ? "上次下载推荐：\(value.recommendedDownload)路；\(MeasurementTimestamp.text(value.evaluatedAt))。换网后旧推荐不生效。"
      : "当前网络尚无可靠评估，默认下载并发为 8"
  }
  @objc private func savePreferences() {
    guard task == nil, var value = baseline, let l = UInt32(latency.stringValue),
      let e = UInt32(exit.stringValue), let d = UInt32(download.stringValue), (1...100).contains(l),
      (1...32).contains(e), (1...8).contains(d)
    else {
      status.stringValue = "请填写有效范围内的整数"
      return
    }
    value.latency = l
    value.exit = e
    value.download = d
    value.mode = "manual"
    value.latencyMode = ["default", "manual", "auto"][max(0, latencyMode.indexOfSelectedItem)]
    if routes.capabilities.contains("measurement.exit_pool.v1") {
      value.exitMode = exitMode.indexOfSelectedItem == 1 ? "manual" : "auto"
    }
    value.downloadMode = ["default", "manual", "auto"][max(0, downloadMode.indexOfSelectedItem)]
    task = Task { [weak self, routes] in
      do {
        if case .preferences(let saved) = try await routes.measurement(.savePreferences(value)) {
          self?.baseline = saved
          self?.render(saved)
          self?.status.stringValue =
            routes.capabilities.contains("measurement.exit_pool.v1")
            ? "已保存；出口影响待发请求，延迟和下载下批生效" : "已保存，下批任务生效"
        }
      } catch { self?.status.stringValue = ProductErrorPresentation.display(error) }
      self?.task = nil
      self?.observe()
    }
  }
  @objc private func resetPreferences() {
    guard task == nil, let baseline else { return }
    latency.stringValue = String(baseline.latency)
    exit.stringValue = String(baseline.exit)
    download.stringValue = String(baseline.download)
    for picker in [latencyMode, exitMode, downloadMode] { picker.selectItem(at: 0) }
    if routes.capabilities.contains("measurement.exit_pool.v1") { exit.stringValue = "8" }
    savePreferences()
  }
  @objc private func evaluate() {
    guard task == nil, routes.connected, routes.downloadOperationID.isEmpty else { return }
    let targets = Array(
      routes.visibleRoutes.filter { !$0.hidden && $0.testSummary?.success == true }.prefix(8))
    guard targets.count >= 2 else {
      status.stringValue = "请先测量至少两条可用线路"
      return
    }
    task = Task { [weak self, routes] in
      do {
        if case .operation(let id) = try await routes.measurement(.evaluate(targets)) {
          self?.assessmentID = id
          self?.status.stringValue = "后台评估中，可停止；完成后重新读取推荐"
        }
      } catch { self?.status.stringValue = ProductErrorPresentation.display(error) }
      self?.task = nil
      self?.observe()
    }
  }
  @objc private func stopAssessment() {
    guard let id = assessmentID else { return }
    Task { [weak self, routes] in
      do { try await routes.cancelMeasurementOperation(id) } catch {
        self?.status.stringValue = ProductErrorPresentation.display(error)
      }
    }
  }
}
