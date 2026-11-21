import AppKit
import Observation
import PaxCore
import PaxUI
import UniformTypeIdentifiers

@MainActor final class InterfaceLanguageSettingsController: NSViewController {
  private let language: InterfaceLanguageStore
  private let picker = PaxRoutePicker(style: .toolbar)
  private let status = PaxText.label("", font: PaxTheme.Typography.caption(), wrapping: true)
  private let source = PaxText.label("", font: PaxTheme.Typography.caption(), wrapping: true)
  private var group: PaxPreferenceGroup?
  private var languageRow: PaxPreferenceRow?
  private var packRow: PaxPreferenceRow?
  private lazy var actions = PaxRouteButton(
    "", symbol: "ellipsis", target: self, action: #selector(showActions))

  init(language: InterfaceLanguageStore) {
    self.language = language
    super.init(nibName: nil, bundle: nil)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    picker.addItems(withTitles: InterfaceLanguageChoice.allCases.map(\.fixedTitle))
    picker.target = self
    picker.action = #selector(selectLanguage)
    picker.setAccessibilityIdentifier("interface-language")
    actions.setAccessibilityIdentifier("language-pack-actions")
    actions.showsDisclosure = true
    let row = PaxPreferenceRow(
      title: "", detail: "", control: picker, style: .compact)
    let actionRow = PaxPreferenceRow(title: "", detail: "", control: actions, style: .compact)
    languageRow = row
    packRow = actionRow
    let footer = NSStackView(views: [source, status])
    footer.orientation = .vertical
    footer.alignment = .leading
    footer.spacing = PaxTheme.Spacing.tight
    for field in [source, status] {
      field.widthAnchor.constraint(equalTo: footer.widthAnchor).isActive = true
    }
    let group = PaxPreferenceGroup(
      title: "", symbol: "globe", rows: [row, actionRow], style: .compact, footer: footer)
    self.group = group
    view = group
    observe()
  }
  private func observe() {
    withObservationTracking {
      render()
    } onChange: { [weak self] in
      Task { @MainActor in self?.observe() }
    }
  }
  private func render() {
    let c = language.context
    group?.apply(title: c.text("settings.interface.title"))
    let title = c.language == .english ? "Language" : "界面语言 / Language"
    languageRow?.apply(title: title, detail: c.text("settings.interface.description"))
    picker.setAccessibilityLabel(title)
    for (index, choice) in InterfaceLanguageChoice.allCases.enumerated() {
      let title = c.language == .english ? englishTitle(choice) : choice.fixedTitle
      picker.item(at: index)?.title = title
    }
    picker.invalidateIntrinsicContentSize()
    picker.selectItem(at: InterfaceLanguageChoice.allCases.firstIndex(of: language.choice) ?? 0)
    let pack = language.installed[c.language]
    let label =
      c.customVersion.map {
        c.text("settings.interface.custom") + ($0.isEmpty ? "" : " · " + $0)
      } ?? c.text("settings.interface.builtin")
    packRow?.apply(title: c.text("settings.interface.pack"), detail: label)
    actions.title = c.text("settings.interface.actions")
    actions.isEnabled = !language.changingPack
    source.stringValue = c.text(
      "settings.interface.current", ["language": languageName(c.language, context: c)])
    if pack?.problem != nil { status.stringValue = c.text("settings.interface.pack_problem") }
    status.isHidden = status.stringValue.isEmpty
  }
  private func englishTitle(_ choice: InterfaceLanguageChoice) -> String {
    switch choice {
    case .system: "System Default"
    case .chinese: "Simplified Chinese"
    case .english: "English"
    }
  }
  private func languageName(_ locale: InterfaceLanguage, context: PresentationContext) -> String {
    context.language == .english
      ? englishTitle(locale == .chinese ? .chinese : .english) : locale.nativeName
  }
  private func restoreTitle(context: PresentationContext) -> String {
    context.language == .english ? "Restore Built-in" : "恢复内置 / Restore Built-in"
  }
  @objc private func selectLanguage() {
    guard InterfaceLanguageChoice.allCases.indices.contains(picker.indexOfSelectedItem) else {
      return
    }
    status.stringValue = ""
    language.choose(InterfaceLanguageChoice.allCases[picker.indexOfSelectedItem])
  }
  @objc private func showActions() {
    let c = language.context
    let menu = NSMenu()
    for (title, selector) in [
      (c.text("settings.interface.export"), #selector(exportPack)),
      (c.text("settings.interface.import"), #selector(importPack)),
      (restoreTitle(context: c), #selector(restorePack)),
    ] {
      let item = menu.addItem(withTitle: title, action: selector, keyEquivalent: "")
      item.target = self
      if selector == #selector(restorePack) {
        item.isEnabled = language.installed[c.language]?.exists == true
      }
    }
    menu.autoenablesItems = false
    menu.popUp(positioning: nil, at: NSPoint(x: 0, y: actions.bounds.maxY), in: actions)
  }
  private var fileType: UTType { UTType(filenameExtension: "paxlang") ?? .json }
  @objc private func exportPack() {
    guard let window = view.window else { return }
    let context = language.context
    let panel = NSSavePanel()
    panel.allowedContentTypes = [fileType]
    panel.nameFieldStringValue = "pax-connect-" + context.language.rawValue + ".paxlang"
    panel.beginSheetModal(for: window) { [weak self] response in
      guard response == .OK, let url = panel.url else { return }
      do { try context.exportPack().encoded().write(to: url, options: .atomic) } catch {
        self?.failed(error, context: context)
      }
    }
  }
  @objc private func importPack() {
    guard let window = view.window else { return }
    let context = language.context
    let panel = NSOpenPanel()
    panel.allowedContentTypes = [fileType, .json]
    panel.allowsMultipleSelection = false
    panel.canChooseDirectories = false
    panel.beginSheetModal(for: window) { [weak self] response in
      guard response == .OK, let file = panel.url, let self else { return }
      Task {
        do {
          let candidate = try await self.language.preview(file)
          self.confirmImport(candidate, context: context)
        } catch { self.failed(error, context: context) }
      }
    }
  }
  private func confirmImport(_ candidate: LanguagePackCandidate, context: PresentationContext) {
    guard let window = view.window else { return }
    let alert = NSAlert()
    let replacing = language.installed[candidate.pack.locale]?.exists == true
    alert.messageText = context.text(
      replacing ? "settings.interface.replace" : "settings.interface.apply")
    alert.informativeText = context.text(
      "settings.interface.preview",
      [
        "language": languageName(candidate.pack.locale, context: context),
        "version": candidate.pack.packVersion ?? "—",
        "entries": candidate.pack.messages.count.formatted(),
        "inherited": candidate.inheritedCount.formatted(),
        "ignored": candidate.ignoredKeys.count.formatted(),
      ])
    if !candidate.ignoredKeys.isEmpty {
      alert.informativeText += "\n" + candidate.ignoredKeys.prefix(12).joined(separator: "\n")
    }
    alert.addButton(withTitle: context.text("common.cancel"))
    alert.addButton(withTitle: alert.messageText)
    alert.beginSheetModal(for: window) { [weak self] response in
      guard response == .alertSecondButtonReturn, let self else { return }
      Task {
        do {
          try await self.language.install(candidate)
          self.status.stringValue = self.language.context.text(
            "settings.interface.installed",
            ["language": self.languageName(candidate.pack.locale, context: self.language.context)])
          self.status.isHidden = false
        } catch { self.failed(error, context: context) }
      }
    }
  }
  @objc private func restorePack() {
    guard let window = view.window else { return }
    let context = language.context
    let alert = NSAlert()
    alert.messageText = restoreTitle(context: context)
    alert.informativeText = context.text("settings.interface.restore_detail")
    alert.addButton(withTitle: context.text("common.cancel"))
    alert.addButton(withTitle: alert.messageText)
    alert.beginSheetModal(for: window) { [weak self] response in
      guard response == .alertSecondButtonReturn, let self else { return }
      Task {
        do {
          try await self.language.restore(context.language)
          self.status.stringValue = self.language.context.text("settings.interface.restored")
          self.status.isHidden = false
        } catch { self.failed(error, context: context) }
      }
    }
  }
  private func failed(_ error: any Error, context: PresentationContext) {
    status.stringValue =
      context.text("settings.interface.failed") + ": " + error.localizedDescription
    status.isHidden = false
  }
}
