import AppKit
import Observation
import PaxCore
import PaxUI

@MainActor
final class AIProfileEditor: WorkbenchModalController, NSTextFieldDelegate {
  let store: AIUsageStore
  let profile: AIProfile?
  let actions = AIUsageActions()
  private let headerTags = AIUsageLayout.stack([], vertical: false)
  private let tabs = PaxTabBar(style: .detail)
  var tab: Tab
  private var setupViews: [NSView] = []
  private var billingViews: [NSView] = []
  var showUnusedModels = false
  enum Section { case reprice }
  enum Tab: String { case setup, billing }
  let scrollTarget: Section?
  var scrolled = false
  private var form: NSView?
  private var editRevision: UInt64 = 0
  var editBaseline: AIProfile?
  private var name: NSTextField?
  private var endpoint: NSTextField?
  var apiKey: PaxRouteSecureField?
  var multiplier: NSTextField?
  private var modified = false
  var saving = false
  var saveButton: NSButton?
  let rates: AIModelRateList
  var ratesCard: NSView?
  var repriceCard: NSView?
  var repriceControl: AIRepriceControl?
  var todayBanner: NSView?
  var todayControl: AIRepriceControl?
  var multiplierErrorShown = false
  private var dataTask: Task<Void, Never>?
  var current: AIProfile? { store.profiles.first { $0.id == profile?.id } ?? profile }
  init(store: AIUsageStore, profile: AIProfile? = nil, scrollTo: Section? = nil) {
    self.store = store
    self.profile = profile
    scrollTarget = scrollTo
    tab = scrollTo == .reprice ? .billing : .setup
    rates = AIModelRateList(overrides: profile?.modelMultipliers ?? [:])
    editRevision = profile?.revision ?? 0
    editBaseline = profile
    super.init(
      title: profile?.name ?? "添加 Profile",
      size: PaxTheme.Routes.dialog, style: .detail, symbol: "key.horizontal")
  }
  override var localizedTitleKey: String? { profile == nil ? "profile_editor.add" : nil }
  override var maximumPresentationWidth: CGFloat? { PaxTheme.Routes.dialog.width }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    super.loadView()
    rates.context = interfaceCopy.context
    pane.identity.addArrangedSubview(headerTags)
    headerTags.spacing = PaxTheme.Spacing.tight
    pane.header.addArrangedSubview(tabs)
    tabs.widthAnchor.constraint(equalTo: pane.header.widthAnchor).isActive = true
    tabs.onSelect = { [weak self] id in
      guard let self, let next = Tab(rawValue: id), next != tab else { return }
      tab = next
      applyTab()
      pane.bodyScroll.contentView.scroll(to: .zero)
    }
    buildForm()
    render()
    observe()
    if let p = current, p.kind != "native", p.modelsState.isEmpty, !store.preview {
      dataTask = Task { [weak self] in
        guard let self else { return }
        _ = await store.configuration.refreshModels(p.id)
      }
    }
  }
  override func viewDidLayout() {
    super.viewDidLayout()
    scrollToRequestedSection()
  }
  override func viewDidDisappear() {
    super.viewDidDisappear()
    dataTask?.cancel()
  }
  private func observe() {
    withObservationTracking {
      if let latest = current,
        latest.connectionRevision == (editBaseline?.connectionRevision ?? 0),
        latest.name == editBaseline?.name, latest.endpoint == editBaseline?.endpoint,
        latest.multiplier == editBaseline?.multiplier
      {
        editRevision = latest.revision
      }
      if let latest = current, !hasDraftChanges {
        name?.stringValue = latest.name
        endpoint?.stringValue = latest.endpoint
        multiplier?.stringValue = AIUsageFormatting.multiplierInput(latest.multiplier)
        rates.reset(to: latest.modelMultipliers)
        editRevision = latest.revision
        editBaseline = latest
      }
      _ = store.profiles
      _ = store.configuration.loading
      render()
    } onChange: { [weak self] in
      Task { @MainActor in self?.observe() }
    }
  }
  private func buildForm() {
    let name = field(
      self.interfaceCopy.text("profile_editor.name"), value: profile?.name ?? "",
      identifier: "ai-profile-name")
    let endpoint = field(
      self.interfaceCopy.text("profile_editor.endpoint"), value: profile?.endpoint ?? "https://",
      identifier: "ai-profile-endpoint")
    let rate = field(
      self.interfaceCopy.text("profile_editor.multiplier"),
      value: AIUsageFormatting.multiplierInput(editBaseline?.multiplier ?? 1),
      identifier: "ai-profile-rate")
    self.name = name.1
    self.endpoint = endpoint.1
    multiplier = rate.1
    let native = profile?.kind == "native"
    let basic = PaxFormGrid(
      rows: native ? [[name.0, rate.0]] : [[name.0, rate.0], [endpoint.0, NSView()]],
      spanning: native ? [] : [1])
    let fields = AIUsageLayout.stack([basic])
    for row in [name, endpoint, rate] {
      row.1.delegate = self
      row.1.target = self
      row.1.action = #selector(markModified)
    }
    rates.onEdit = { [weak self] in self?.draftChanged() }
    rates.onLayoutChange = { [weak self] in self?.pane.invalidateContentSize() }
    let key = PaxRouteSecureField(
      placeholder: profile?.hasKey == true
        ? self.interfaceCopy.text("profile_editor.key_saved")
        : self.interfaceCopy.text("profile_editor.key_input"))
    key.setAccessibilityIdentifier("ai-profile-key")
    key.delegate = self
    key.target = self
    key.action = #selector(markModified)
    apiKey = key
    if !native {
      // Same left/right edges as the API address field above, whatever the grid insets are.
      let title = AIUsageLayout.label("API Key", secondary: true)
      title.font = PaxTheme.Typography.caption()
      let holder = NSView()
      for item in [title, key] {
        item.translatesAutoresizingMaskIntoConstraints = false
        holder.addSubview(item)
      }
      fields.addArrangedSubview(holder)
      NSLayoutConstraint.activate([
        holder.widthAnchor.constraint(equalTo: fields.widthAnchor),
        title.leadingAnchor.constraint(equalTo: endpoint.1.leadingAnchor),
        title.topAnchor.constraint(equalTo: holder.topAnchor),
        key.topAnchor.constraint(equalTo: title.bottomAnchor, constant: PaxTheme.Spacing.tight),
        key.leadingAnchor.constraint(equalTo: endpoint.1.leadingAnchor),
        key.trailingAnchor.constraint(equalTo: endpoint.1.trailingAnchor),
        key.bottomAnchor.constraint(equalTo: holder.bottomAnchor),
      ])
    } else {
      fields.addArrangedSubview(
        AIUsageLayout.label(
          self.interfaceCopy.text("profile_editor.native_prefix")
            + (profile?.nativeCLI?.rawValue
              ?? self.interfaceCopy.text("profile_editor.native_client"))
            + self.interfaceCopy.text("profile_editor.native_suffix"),
          secondary: true))
    }
    form = profileSection(
      self.interfaceCopy.text("profile_editor.connection"), symbol: "network",
      views: native
        ? [fields]
        : [
          fields,
          AIUsageLayout.label(
            self.interfaceCopy.text("profile_editor.save_detail"), secondary: true),
        ])
  }
  private func renderHeader() {
    for v in headerTags.arrangedSubviews {
      headerTags.removeArrangedSubview(v)
      v.removeFromSuperview()
    }
    let kind =
      profile?.kind == "native"
      ? self.interfaceCopy.text("profile_editor.native_configuration") : "API Profile"
    headerTags.addArrangedSubview(
      PaxText.label(kind, font: PaxTheme.Typography.caption(), secondary: true))
    if let p = current {
      headerTags.addArrangedSubview(
        PaxFeatureTag(AIUsageFormatting.multiplier(p.multiplier), tone: .blue))
      let status = store.profileStatus(p)
      headerTags.addArrangedSubview(PaxBadge(title: status.text, tone: status.tone))
    }
  }
  func render() {
    guard isViewLoaded else { return }
    let offset = pane.bodyScroll.contentView.bounds.origin
    renderHeader()
    pane.clear()
    if let todayBanner { pane.add(todayBanner) }
    setupViews = [form].compactMap { $0 } + profileDetailViews()
    billingViews = current.map(profileRateSections) ?? []
    for view in setupViews + billingViews { pane.add(view) }
    applyTab()
    let save = actions.button(
      saving
        ? self.interfaceCopy.text("profile_editor.saving")
        : self.interfaceCopy.text("profile_editor.save"), primary: true, enabled: !saving
    ) {
      [weak self] in self?.save()
    }
    saveButton = save
    let close = actions.button(self.interfaceCopy.text("profile_editor.close")) { [weak self] in
      self?.cancelOperation(nil)
    }
    close.widthAnchor.constraint(
      greaterThanOrEqualToConstant: ceil(close.intrinsicContentSize.width) + PaxTheme.Spacing.xs
    ).isActive = true
    pane.setActions([close, save])
    syncDraftState()
    pane.bodyScroll.contentView.scroll(to: offset)
  }
  /// Shows one tab's cards; the other tab stays built (hidden) so drafts survive switching.
  func applyTab() {
    tabs.isHidden = current == nil
    tabs.apply(
      [
        .init(
          id: Tab.setup.rawValue, title: self.interfaceCopy.text("profile_editor.configuration_tab")
        ),
        .init(
          id: Tab.billing.rawValue, title: self.interfaceCopy.text("profile_editor.multiplier")),
      ],
      selected: tab.rawValue)
    pane.performContentUpdates {
      for view in setupViews { view.isHidden = tab != .setup && current != nil }
      for view in billingViews { view.isHidden = tab != .billing }
    }
  }
  private var hasDraftChanges: Bool {
    modified || !(apiKey?.stringValue.isEmpty ?? true)
      || name?.stringValue != (editBaseline?.name ?? "")
      || endpoint?.stringValue != (editBaseline?.endpoint ?? "https://")
      || multiplierDraftChanged
  }
  func controlTextDidChange(_ notification: Notification) {
    modified = true
    if (notification.object as? NSTextField) === multiplier { draftChanged() }
  }
  @objc private func markModified() { modified = true }
  private func save() {
    guard !saving else { return }
    guard inputsValid, case .value(let rate) = defaultInput, let overrides = rates.overrides else {
      syncDraftState()
      return
    }
    saving = true
    let p = current
    var value = AIProfile(
      id: p?.id ?? UUID().uuidString, name: name?.stringValue ?? "",
      endpoint: endpoint?.stringValue ?? "",
      multiplier: rate, modelIDs: p?.modelIDs ?? [],
      revision: editRevision,
      hasKey: p?.hasKey ?? false, kind: p?.kind ?? "", nativeCLI: p?.nativeCLI)
    value.modelMultipliers = overrides  // saving replaces the whole map; empty fields are omitted
    let offerSync = p != nil && (rate != p?.multiplier || overrides != p?.modelMultipliers)
    let key = apiKey?.stringValue ?? ""
    Task { [weak self] in
      guard let self else { return }
      let ok = await store.configuration.save(value, key: key)
      saving = false
      if ok, offerSync, let latest = current {
        saved(latest)
      } else if ok {
        apiKey?.stringValue = ""
        modified = false
        superClose()
      } else {
        multiplierErrorShown = false
        pane.status.stringValue =
          store.configuration.error(context: interfaceCopy.context)
          ?? self.interfaceCopy.text("profile_editor.save_failed")
        render()
      }
    }
  }
  /// Moves the baseline to the saved profile, keeping the editor open.
  func markSaved(_ latest: AIProfile) {
    modified = false
    editBaseline = latest
    editRevision = latest.revision
    multiplier?.stringValue = AIUsageFormatting.multiplierInput(latest.multiplier)
    rates.reset(to: latest.modelMultipliers)
  }
  private func superClose() { super.close() }
  override func cancelOperation(_ sender: Any?) {
    guard !saving else { return }
    guard hasDraftChanges else {
      apiKey?.stringValue = ""
      super.close()
      return
    }
    multiplierErrorShown = false
    pane.status.stringValue = self.interfaceCopy.text("profile_editor.unsaved")
    pane.setActions([
      actions.button(self.interfaceCopy.text("profile_editor.keep_editing")) { [weak self] in
        self?.pane.status.stringValue = ""
        self?.render()
      },
      actions.button(self.interfaceCopy.text("profile_editor.discard")) { [weak self] in
        self?.apiKey?.stringValue = ""
        self?.superClose()
      },
    ])
  }
  override func close() { cancelOperation(nil) }
}
