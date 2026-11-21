import AppKit
import Observation
import PaxCore
import PaxUI

@MainActor
final class AIBindingController: WorkbenchModalController {
  static let popoverWidth: CGFloat = 420
  private let store: AIUsageStore
  private let client: AIClient
  private let targetProfile: String?
  private let actions = AIUsageActions()
  var onDismiss: (() -> Void)?
  private var applying = false
  private var applyButtons: [NSButton] = []
  private let model = PaxRouteField("")
  private let explanation = PaxText.label(
    "", font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  init(store: AIUsageStore, client: AIClient, targetProfile: String? = nil) {
    self.store = store
    self.client = client
    self.targetProfile = targetProfile
    super.init(title: "", style: .compact, symbol: client.symbol)
  }
  override var maximumPresentationWidth: CGFloat? { Self.popoverWidth }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    super.loadView()
    pane.applyTitle(
      interfaceCopy.text("ai_usage.detail.configuration", ["client": client.rawValue]))
    render()
    observeAvailability()
  }
  private func observeAvailability() {
    withObservationTracking {
      let enabled = store.preview || (store.configuration.available && !store.configuration.loading)
      for button in applyButtons { button.isEnabled = enabled }
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observeAvailability() }
    }
  }
  override func viewDidAppear() {
    super.viewDidAppear()
    guard !store.preview else { return }
    Task { [weak self] in
      guard let self else { return }
      await store.configuration.refresh()
      if !applying { render() }
    }
  }
  private func note(_ text: String) -> NSTextField {
    PaxText.label(text, font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  }
  private func render() {
    pane.clear()
    actions.reset()
    applyButtons.removeAll()
    let binding = store.bindings[client]
    var configurationRows: [NSView] = []
    let status = PaxBadge(
      title: AIProfilesStore.stateLabel(binding, context: self.interfaceCopy.context),
      tone: AIUsageStore.tone(forState: binding?.state ?? ""))
    let recheck = actions.button(self.interfaceCopy.text("action.profile.recheck")) { [weak self] in
      self?.recheck()
    }
    recheck.capsule = false
    let stateRow = AIUsageLayout.stack(
      [status, NSView()] + (store.preview ? [] : [recheck]), vertical: false)
    configurationRows.append(stateRow)
    if let binding, !binding.targetPath.isEmpty {
      configurationRows.append(note(binding.targetPath))
    }
    if !store.preview {
      switch binding?.state {
      case "ready", "changed":
        let button = actions.button(self.interfaceCopy.text("action.common.import")) {
          [weak self] in self?.importForm()
        }
        button.capsule = false
        configurationRows.append(
          PaxPreferenceRow(
            title: self.interfaceCopy.text("interface_detail.b1b6fffedb"),
            detail: self.interfaceCopy.text("interface_detail.d6d612e184"), control: button))
      case "missing":
        configurationRows.append(note(self.interfaceCopy.text("interface_detail.a0ff626fc2")))
      case "account":
        configurationRows.append(note(self.interfaceCopy.text("interface_detail.a46dd9cbed")))
      case "invalid", "recovery_required":
        configurationRows.append(note(self.interfaceCopy.text("interface_detail.93daea2046")))
      default: break
      }
    }
    pane.add(
      PaxPreferenceGroup(
        title: self.interfaceCopy.text("interface_detail.f92335ddcd"), symbol: "terminal",
        rows: [AIUsageLayout.stack(configurationRows)]))

    let accountHelp = actions.button(self.interfaceCopy.text("action.profile.login_help")) {
      [weak self] in self?.showAccountHelp()
    }
    accountHelp.capsule = false
    pane.add(
      PaxPreferenceGroup(
        title: self.interfaceCopy.text("interface_detail.57adc05c18"), symbol: "person.crop.circle",
        rows: [
          PaxPreferenceRow(
            title: self.interfaceCopy.text("interface_detail.159900380f"),
            detail: self.interfaceCopy.text("interface_detail.8c6a4fd295"),
            control: accountHelp)
        ]))

    let profiles = store.profiles.filter {
      (targetProfile == nil || targetProfile == $0.id)
        && ($0.kind != "native" || $0.nativeCLI == client)
    }
    let profileRows: [NSView] = profiles.map { p in
      let current = binding?.profileID == p.id
      let applied = current && binding?.state == "applied"
      let control: NSView
      if applied {
        control = PaxBadge(title: self.interfaceCopy.text("profile_state.applied"), tone: .healthy)
      } else {
        let button = actions.button(
          self.interfaceCopy.text("action.profile.apply"),
          enabled: store.preview
            || (store.configuration.available && !store.configuration.loading
              && ["missing", "ready", "applied", "pending", "changed"].contains(
                binding?.state ?? ""))
        ) { [weak self] in self?.prepare(p) }
        button.capsule = false
        if ["missing", "ready", "applied", "pending", "changed"].contains(binding?.state ?? "") {
          applyButtons.append(button)
        }
        button.setAccessibilityLabel(
          self.interfaceCopy.text("interface_detail.d32f93aa8f") + p.name)
        control = button
      }
      let source =
        p.kind == "native"
        ? self.interfaceCopy.text("interface_detail.d1d072d54a")
        : p.serviceSource.label(context: self.interfaceCopy.context) + " · "
          + AIUsageStore.host(p.endpoint)
      let detail =
        source + " · " + AIUsageFormatting.multiplier(p.multiplier)
        + (current && !applied
          ? " · " + AIProfilesStore.stateLabel(binding, context: self.interfaceCopy.context) : "")
      return PaxPreferenceRow(title: p.name, detail: detail, control: control)
    }
    pane.add(
      PaxPreferenceGroup(
        title: self.interfaceCopy.text("interface_detail.943b60315f"), symbol: "square.stack",
        rows: profileRows.isEmpty
          ? [note(self.interfaceCopy.text("interface_detail.eddd397d9b"))] : profileRows))
    if !store.preview {
      let create = actions.button(self.interfaceCopy.text("action.common.create_ellipsis")) {
        [weak self] in self?.createForm()
      }
      create.capsule = false
      pane.add(
        PaxPreferenceGroup(
          title: self.interfaceCopy.text("interface_detail.42fa3c6538"), symbol: "plus.circle",
          rows: [
            PaxPreferenceRow(
              title: self.interfaceCopy.text("interface_detail.4a548bc641"),
              detail: self.interfaceCopy.text("interface_detail.ca15b55300"), control: create)
          ]))
    }
    explanation.stringValue = self.interfaceCopy.text("interface_detail.db594d56e0")
    pane.add(explanation)
    pane.setActions([
      actions.button(self.interfaceCopy.text("common.close")) { [weak self] in self?.close() }
    ])
    pane.invalidateContentSize()
  }
  private func showAccountHelp() {
    pane.clear()
    let login =
      client == .codex
      ? self.interfaceCopy.text("interface_detail.ae4e4b9dd0")
      : self.interfaceCopy.text("interface_detail.df1038363d")
    pane.add(
      PaxPreferenceGroup(
        title: self.interfaceCopy.text("interface_detail.4276678907"), symbol: "person.crop.circle",
        rows: [note(login)]))
    pane.add(
      PaxPreferenceGroup(
        title: self.interfaceCopy.text("interface_detail.9cbd309083"), symbol: "key",
        rows: [note(self.interfaceCopy.text("interface_detail.e08dd04429"))]))
    pane.add(
      PaxPreferenceGroup(
        title: self.interfaceCopy.text("interface_detail.dcf264eaf4"), symbol: "chart.bar",
        rows: [note(self.interfaceCopy.text("interface_detail.e55de60c0f"))]))
    let recheck = actions.button(self.interfaceCopy.text("action.profile.recheck"), primary: true) {
      [weak self] in self?.recheck()
    }
    recheck.widthAnchor.constraint(
      greaterThanOrEqualToConstant: ceil(recheck.intrinsicContentSize.width) + PaxTheme.Spacing.xs
    ).isActive = true
    pane.setActions([
      actions.button(self.interfaceCopy.text("action.common.back")) { [weak self] in self?.render()
      }, recheck,
    ])
    pane.invalidateContentSize()
  }
  private func recheck() {
    guard !applying else { return }
    applying = true
    Task { [weak self] in
      guard let self else { return }
      await store.configuration.refresh()
      applying = false
      render()
      if let error = store.configuration.error { report(error, failed: true) }
    }
  }
  private func createForm() {
    pane.clear()
    let name = field(
      self.interfaceCopy.text("profile_editor.name"), value: "", identifier: "ai-create-name")
    let endpoint = field(
      self.interfaceCopy.text("profile_editor.endpoint"), value: "https://",
      identifier: "ai-create-endpoint")
    let rate = field(
      self.interfaceCopy.text("profile_editor.multiplier"), value: "1", identifier: "ai-create-rate"
    )
    let key = PaxRouteSecureField(placeholder: self.interfaceCopy.text("profile_editor.key_input"))
    key.setAccessibilityIdentifier("ai-create-key")
    for view in [name.0, endpoint.0, rate.0, note("API Key"), key] { pane.add(view) }
    pane.add(note(self.interfaceCopy.text("interface_detail.a15de3671b")))
    pane.add(explanation)
    explanation.stringValue = ""
    pane.setActions([
      actions.button(self.interfaceCopy.text("action.common.back")) { [weak self] in
        key.stringValue = ""
        self?.render()
      },
      actions.button(self.interfaceCopy.text("profile_editor.save"), primary: true) { [weak self] in
        guard let self, !applying else { return }
        guard let multiplier = Double(rate.1.stringValue) else {
          report(self.interfaceCopy.text("interface_detail.847a995d32"), failed: true)
          return
        }
        let profile = AIProfile(
          id: UUID().uuidString,
          name: name.1.stringValue.trimmingCharacters(in: .whitespacesAndNewlines),
          endpoint: endpoint.1.stringValue, multiplier: multiplier)
        let secret = key.stringValue
        applying = true
        Task { [weak self] in
          guard let self else { return }
          let ok = await store.configuration.save(profile, key: secret)
          applying = false
          if ok {
            key.stringValue = ""
            render()
            report(self.interfaceCopy.text("interface_detail.d9fd5776f7"))
          } else {
            report(
              store.configuration.error(context: interfaceCopy.context)
                ?? self.interfaceCopy.text("profile_editor.save_failed"), failed: true)
          }
        }
      },
    ])
    pane.invalidateContentSize()
  }
  private func importForm() {
    guard let binding = store.bindings[client] else { return }
    pane.clear()
    let host = URL(string: binding.endpoint)?.host ?? client.rawValue
    let name = field(
      self.interfaceCopy.text("profile_editor.name"), value: host, identifier: "ai-import-name")
    let rate = field(
      self.interfaceCopy.text("profile_editor.multiplier"), value: "1", identifier: "ai-import-rate"
    )
    pane.add(
      note(
        self.interfaceCopy.text("interface_detail.acbb76a2b1") + client.rawValue
          + self.interfaceCopy.text("interface_detail.2c5cd6e679") + binding.endpoint))
    pane.add(note(self.interfaceCopy.text("interface_detail.ffff3d9e3a")))
    pane.add(name.0)
    pane.add(rate.0)
    pane.add(explanation)
    explanation.stringValue = ""
    pane.setActions([
      actions.button(self.interfaceCopy.text("action.common.back")) { [weak self] in self?.render()
      },
      actions.button(self.interfaceCopy.text("action.profile.confirm_import"), primary: true) {
        [weak self] in
        guard let self, !applying else { return }
        guard let multiplier = Double(rate.1.stringValue) else {
          report(self.interfaceCopy.text("interface_detail.847a995d32"), failed: true)
          return
        }
        applying = true
        Task { [weak self] in
          guard let self else { return }
          let ok = await store.configuration.importCurrent(
            client, name: name.1.stringValue, multiplier: multiplier)
          applying = false
          if ok {
            render()
            report(self.interfaceCopy.text("interface_detail.d892ea1dfc"))
          } else {
            report(
              store.configuration.error(context: interfaceCopy.context)
                ?? self.interfaceCopy.text("interface_detail.bff9ebfda6"),
              failed: true)
          }
        }
      },
    ])
    pane.invalidateContentSize()
  }
  private func prepare(_ profile: AIProfile) {
    guard !applying else { return }
    if store.preview {
      store.configuration.previewBind(client, profile: profile)
      close()
      return
    }
    if store.bindings[client]?.state == "missing" {
      pane.clear()
      pane.add(
        note(
          self.interfaceCopy.text("interface_detail.7c59e47583") + profile.name
            + self.interfaceCopy.text("interface_detail.eb3ff5d80f") + client.rawValue + "。"))
      pane.add(note(store.bindings[client]?.targetPath ?? ""))
      if store.bindings[client]?.requiresModel == true {
        pane.add(note(self.interfaceCopy.text("interface_detail.1ac3c024c6")))
        model.placeholderString = self.interfaceCopy.text("interface_detail.d4fc4536ac")
        model.setAccessibilityIdentifier("ai-initialize-model")
        pane.add(model)
        if !profile.catalog.isEmpty {
          pane.add(
            note(
              self.interfaceCopy.text("interface_detail.3d91201ff6")
                + profile.catalog.prefix(8).map(\.id).joined(separator: "、")))
        }
      }
      pane.add(note(self.interfaceCopy.text("interface_detail.1fae8249e2")))
      pane.add(explanation)
      explanation.stringValue = ""
      pane.setActions([
        actions.button(self.interfaceCopy.text("action.common.back")) { [weak self] in
          self?.render()
        },
        actions.button(self.interfaceCopy.text("action.profile.create_apply"), primary: true) {
          [weak self] in
          self?.apply(profile, initialize: true)
        },
      ])
      pane.invalidateContentSize()
    } else {
      apply(profile, initialize: false)
    }
  }
  private func apply(_ profile: AIProfile, initialize: Bool) {
    guard !applying else { return }
    applying = true
    report(self.interfaceCopy.text("interface_detail.a1e948d633"))
    Task { [weak self] in
      guard let self else { return }
      defer { applying = false }
      await store.configuration.refresh()
      guard
        let plan = await store.configuration.plan(
          client, profile: profile, initialize: initialize, modelID: model.stringValue)
      else {
        report(
          store.configuration.error(context: interfaceCopy.context)
            ?? self.interfaceCopy.text("interface_detail.e803e144d5"),
          failed: true)
        return
      }
      let ok = await store.configuration.apply(plan)
      if ok {
        render()
        report(self.interfaceCopy.text("interface_detail.f13295200d"))
      } else {
        report(
          store.configuration.error(context: interfaceCopy.context)
            ?? self.interfaceCopy.text("interface_detail.b387bce5c6"), failed: true)
      }
    }
  }
  private func report(_ text: String, failed: Bool = false) {
    explanation.stringValue = text
    explanation.textColor = failed ? PaxTheme.Color.error : PaxTheme.Color.textSecondary
    pane.invalidateContentSize()
  }
  override func close() {
    guard !applying else { return }
    super.close()
  }
  override func viewDidDisappear() {
    super.viewDidDisappear()
    onDismiss?()
    onDismiss = nil
  }
}
