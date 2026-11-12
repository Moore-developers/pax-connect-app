import AppKit
import PaxCore
import PaxUI

/// Per-model multiplier inputs of one Profile. The view is built once and only rebuilt when the
/// model list changes, so typing is never interrupted; drafts survive a rebuild.
@MainActor
final class AIModelRateList: NSObject, NSTextFieldDelegate {
  let view = AIUsageLayout.stack([])
  var onEdit: (() -> Void)?
  /// The visible family tab changed, so the hosting card's height did too.
  var onLayoutChange: (() -> Void)?
  private(set) var models: [String] = []
  private var drafts: [String: String]
  /// Saved values, kept exact: an untouched field whose text is only a rounded display of a
  /// legacy value (more than 3 decimals) must not rewrite it.
  private var originals: [String: Double]
  private var fields: [String: NSTextField] = [:]
  private var errors: [String: NSTextField] = [:]
  private var families: [Family] = []
  private var panels: [NSView] = []
  private var selectedFamily = ""
  private static let columns = 2

  struct Family: Equatable {
    let title: String
    let models: [String]
  }

  init(overrides: [String: Double]) {
    drafts = overrides.mapValues(AIUsageFormatting.multiplierInput)
    originals = overrides
    super.init()
    view.spacing = PaxTheme.Spacing.tight
  }

  /// Catalog and used models plus any model that already has an override (so saving never
  /// drops one), deduplicated, never "<synthetic>"; most requests first, then by name.
  static func models(catalog: [String], counts: [String: Int], overrides: [String: Double])
    -> [String]
  {
    let ids = Set(catalog).union(counts.filter { $0.value > 0 }.keys).union(overrides.keys)
      .filter { !AIUsageFormatting.isSyntheticModel($0) }
    return ids.sorted {
      let (left, right) = (counts[$0] ?? 0, counts[$1] ?? 0)
      return left != right ? left > right : $0 < $1
    }
  }

  /// Models grouped by vendor family (the letters before the first dash or digit: gpt, claude,
  /// gemini…), biggest first; families of one model fold into a trailing 其他. A single group
  /// means no tabs are needed.
  static func families(_ ids: [String]) -> [Family] {
    var groups: [String: [String]] = [:]
    for id in ids {
      let base = id.split(separator: "/").last.map(String.init) ?? id
      groups[String(base.lowercased().prefix { $0.isLetter }), default: []].append(id)
    }
    let names = [
      "gpt": "GPT", "claude": "Claude", "gemini": "Gemini", "deepseek": "DeepSeek", "glm": "GLM",
      "qwen": "Qwen", "llama": "Llama", "mistral": "Mistral", "grok": "Grok",
    ]
    var result: [Family] = []
    var rest: [String] = []
    let ordered = groups.sorted { ($1.value.count, $0.key) < ($0.value.count, $1.key) }
    for (key, models) in ordered {
      if models.count > 1, key.count > 1 {
        result.append(Family(title: names[key] ?? key.capitalized, models: models))
      } else {
        rest += models
      }
    }
    if !rest.isEmpty {
      var position: [String: Int] = [:]
      for (index, id) in ids.enumerated() where position[id] == nil { position[id] = index }
      let ordered = rest.sorted { (position[$0] ?? 0) < (position[$1] ?? 0) }
      result.append(Family(title: "其他", models: ordered))
    }
    return result
  }

  /// Parsed overrides; nil when any field is invalid. Empty fields are omitted.
  var overrides: [String: Double]? {
    var map: [String: Double] = [:]
    for (id, text) in drafts {
      if let kept = originals[id], text == AIUsageFormatting.multiplierInput(kept) {
        map[id] = kept
        continue
      }
      switch AIMultiplierInput.parse(text) {
      case .empty: continue
      case .value(let value): map[id] = value
      case .invalid: return nil
      }
    }
    return map
  }
  var isValid: Bool { overrides != nil }
  func hasChanges(from saved: [String: Double]) -> Bool {
    guard let overrides else { return true }
    return overrides != saved
  }
  func reset(to saved: [String: Double]) {
    drafts = saved.mapValues(AIUsageFormatting.multiplierInput)
    originals = saved
    for (id, field) in fields { field.stringValue = drafts[id] ?? "" }
    for id in models { showError(id) }
  }

  func update(models next: [String], fallback value: Double) {
    let placeholder = "默认 " + AIUsageFormatting.multiplierInput(value)
    for field in fields.values { field.placeholderString = placeholder }
    guard next != models else { return }
    models = next
    for row in view.arrangedSubviews {
      view.removeArrangedSubview(row)
      row.removeFromSuperview()
    }
    fields = [:]
    errors = [:]
    panels = []
    families = Self.families(next)
    if next.isEmpty {
      view.addArrangedSubview(AIUsageLayout.label("暂无模型：读取模型目录或产生用量后会列在这里", secondary: true))
      return
    }
    if !families.contains(where: { $0.title == selectedFamily }) {
      selectedFamily = families[0].title
    }
    if families.count > 1 {
      let tabs = PaxTabs(families.map(\.title), counts: families.map { $0.models.count })
      tabs.selectedIndex = families.firstIndex { $0.title == selectedFamily } ?? 0
      tabs.setAccessibilityIdentifier("ai-model-rate-families")
      tabs.onSelect = { [weak self] index in self?.select(index) }
      view.addArrangedSubview(AIUsageLayout.wrap(tabs))
    }
    for family in families {
      let panel = AIUsageLayout.stack(
        stride(from: 0, to: family.models.count, by: Self.columns).map { start in
          let cells = family.models[start..<min(start + Self.columns, family.models.count)]
            .map { row($0, placeholder: placeholder) }
          let padded = cells + (0..<(Self.columns - cells.count)).map { _ in NSView() }
          let line = AIUsageLayout.stack(padded, vertical: false, equal: true)
          line.spacing = PaxTheme.Spacing.roomy
          return line
        })
      panel.spacing = PaxTheme.Spacing.tight
      panel.isHidden = family.title != selectedFamily
      view.addArrangedSubview(panel)
      panel.widthAnchor.constraint(equalTo: view.widthAnchor).isActive = true
      panels.append(panel)
    }
  }

  private func select(_ index: Int) {
    guard families.indices.contains(index) else { return }
    selectedFamily = families[index].title
    for (panel, family) in zip(panels, families) { panel.isHidden = family.title != selectedFamily }
    onLayoutChange?()
  }

  private func row(_ id: String, placeholder: String) -> NSView {
    let name = AIUsageLayout.label(id)
    name.lineBreakMode = .byTruncatingMiddle
    name.toolTip = id
    name.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    let input = PaxRouteField(drafts[id] ?? "")
    input.placeholderString = placeholder
    input.alignment = .right
    input.delegate = self
    input.setAccessibilityIdentifier("ai-model-rate-" + id)
    input.setAccessibilityLabel(id + " 倍率")
    input.widthAnchor.constraint(equalToConstant: 96).isActive = true
    fields[id] = input
    let error = AIUsageLayout.label(AIMultiplierInput.errorText, secondary: true)
    error.textColor = PaxTheme.Color.error
    error.isHidden = true
    errors[id] = error
    let line = AIUsageLayout.stack(
      [name, NSView(), error, input, AIUsageLayout.label("×", secondary: true)], vertical: false)
    return line
  }

  func controlTextDidChange(_ notification: Notification) {
    guard let field = notification.object as? NSTextField,
      let id = fields.first(where: { $0.value === field })?.key
    else { return }
    drafts[id] = field.stringValue
    showError(id)
    onEdit?()
  }
  private func showError(_ id: String) {
    errors[id]?.isHidden = AIMultiplierInput.parse(drafts[id] ?? "") != .invalid
  }
}
