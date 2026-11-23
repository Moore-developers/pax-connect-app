import AppKit
import PaxClient
import PaxCore
import PaxUI

@MainActor
final class AntigravityModelsController: WorkbenchModalController {
  private let data: AIUsageDataStore
  private var catalog: AntigravityModels?
  private let plans = ["all", "free_plus", "pro", "ultra", "enterprise"]
  private let filter = NSPopUpButton()
  private let modelRows = AIUsageLayout.stack([])

  init(data: AIUsageDataStore) {
    self.data = data
    super.init(title: "", size: .init(width: 740, height: 620), symbol: "sparkles")
  }

  override var localizedTitleKey: String? { "ai_usage.antigravity.models_title" }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  override func loadView() {
    super.loadView()
    pane.add(note("ai_usage.antigravity.loading"))
    Task { [weak self] in
      guard let self else { return }
      do {
        let catalog = try await self.data.antigravityModels()
        self.catalog = catalog
        self.render(catalog)
      } catch {
        self.pane.clear()
        self.showError(error)
      }
    }
  }

  private func note(_ key: String) -> NSTextField {
    PaxText.label(
      interfaceCopy.text(key), font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  }

  private func render(_ catalog: AntigravityModels) {
    pane.clear()
    let names = Dictionary(uniqueKeysWithValues: catalog.models.map { ($0.id, $0.name) })
    let headings = ["model", "input", "read", "write", "output", "effective"].map {
      AIUsageLayout.label(interfaceCopy.text("ai_usage.antigravity.column." + $0), secondary: true)
    }
    let rateRows = catalog.rates.map { rate in
      [AIUsageLayout.label(names[rate.modelID] ?? rate.modelID)]
        + [rate.input, rate.cacheRead, rate.cacheWrite, rate.output].map { value in
          AIUsageLayout.label(
            value.map { "$" + $0.formatted(.number.precision(.fractionLength(2...4))) } ?? "—",
            numeric: true)
        }
        + [AIUsageLayout.label(rate.effectiveOn.isEmpty ? "—" : rate.effectiveOn, numeric: true)]
    }
    let grid = NSGridView(views: [headings] + rateRows)
    grid.xPlacement = .leading
    grid.rowSpacing = PaxTheme.Spacing.tight
    grid.columnSpacing = PaxTheme.Spacing.regular
    pane.add(
      PaxPreferenceGroup(
        title: interfaceCopy.text("ai_usage.antigravity.rates"), symbol: "dollarsign.circle",
        rows: [note("ai_usage.antigravity.rates_note"), grid]))

    filter.removeAllItems()
    filter.addItems(withTitles: plans.map(planName))
    filter.setAccessibilityLabel(interfaceCopy.text("ai_usage.antigravity.plan"))
    filter.target = self
    filter.action = #selector(updateModels)
    let heading = AIUsageLayout.stack(
      [AIUsageLayout.label(interfaceCopy.text("ai_usage.antigravity.plan")), NSView(), filter],
      vertical: false)
    pane.add(
      PaxPreferenceGroup(
        title: interfaceCopy.text("ai_usage.antigravity.catalog"), symbol: "list.bullet",
        rows: [heading, note("ai_usage.antigravity.catalog_note"), modelRows]))
    pane.add(note("ai_usage.antigravity.coverage"))
    let source = button(
      interfaceCopy.text("ai_usage.antigravity.source"), #selector(openSource))
    pane.add(AIUsageLayout.stack([source, NSView()], vertical: false))
    updateModels()
  }

  private func planName(_ plan: String) -> String {
    interfaceCopy.text("ai_usage.antigravity.plan." + plan)
  }

  @objc private func updateModels() {
    guard let catalog, plans.indices.contains(filter.indexOfSelectedItem) else { return }
    for view in modelRows.arrangedSubviews {
      modelRows.removeArrangedSubview(view)
      view.removeFromSuperview()
    }
    let selected = plans[filter.indexOfSelectedItem]
    for model in catalog.models where selected == "all" || model.plans.contains(selected) {
      let name = AIUsageLayout.label(model.name)
      let detail: String
      if model.kind == "tool" || model.kind == "orchestrator" {
        detail = interfaceCopy.text("ai_usage.antigravity.kind." + model.kind)
      } else if !model.retiredOn.isEmpty {
        detail = interfaceCopy.text(
          "ai_usage.antigravity.retired", ["date": model.retiredOn])
      } else {
        detail = model.plans.map(planName).joined(separator: " · ")
      }
      let row = AIUsageLayout.stack(
        [name, NSView(), AIUsageLayout.label(detail, secondary: true)], vertical: false)
      modelRows.addArrangedSubview(row)
      row.widthAnchor.constraint(equalTo: modelRows.widthAnchor).isActive = true
    }
  }

  @objc private func openSource() {
    if let url = URL(string: "https://cloud.google.com/model-garden/pricing") {
      NSWorkspace.shared.open(url)
    }
  }
}
