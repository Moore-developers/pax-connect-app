import AppKit
import PaxUI

/// 规则库的单元格渲染：包行与规则行各有自己的列语义，与列布局分开维护。
@MainActor
extension RoutingPackagesController {
  func packageCell(_ id: String) -> NSView? {
    guard let pack = store.package(id) else { return nil }
    let title = PaxText.label(
      pack.name, font: PaxTheme.Typography.label(size: PaxTheme.Table.font, weight: .medium))
    title.lineBreakMode = .byTruncatingTail
    title.toolTip = pack.name
    title.textColor = store.selectedPackageID == id ? PaxTheme.Routes.blue : PaxTheme.Color.text
    let summary = pack.kindPresentation.summary
    let sub = PaxText.label(summary, font: PaxTheme.Typography.time, secondary: true)
    let labels = NSStackView(views: [title, sub])
    labels.orientation = .vertical
    labels.alignment = .leading
    labels.spacing = PaxTheme.Routing.packageTextGap
    if [.local, .subscription].contains(pack.kind) {
      let destination = PaxText.label(
        store.destinationName(pack.destination) + (pack.enabled ? "" : " · 已停用"),
        font: PaxTheme.Typography.time, secondary: true)
      if !pack.enabled {
        let text = NSMutableAttributedString(string: destination.stringValue)
        text.addAttribute(
          .foregroundColor, value: PaxTheme.Color.error,
          range: (destination.stringValue as NSString).range(of: "已停用"))
        destination.attributedStringValue = text
      }
      destination.lineBreakMode = .byTruncatingTail
      destination.toolTip = destination.stringValue
      labels.addArrangedSubview(destination)
    }
    for label in labels.arrangedSubviews {
      label.widthAnchor.constraint(equalTo: labels.widthAnchor).isActive = true
      label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    }
    labels.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    labels.setContentHuggingPriority(.defaultLow, for: .horizontal)
    let row = RoutingPackageRow(views: [labels])
    row.distribution = .fill
    row.spacing = PaxTheme.Spacing.xs
    if [.local, .subscription].contains(pack.kind),
      let index = store.packages.firstIndex(where: { $0.id == id })
    {
      let arrows = NSStackView()
      arrows.orientation = .vertical
      arrows.widthAnchor.constraint(equalToConstant: PaxTheme.Routing.moveSize).isActive = true
      arrows.spacing = PaxTheme.Routing.arrowPadding
      for (symbol, delta, text) in [
        ("chevron.up", -1, "上移，提高匹配优先级"), ("chevron.down", 1, "下移，降低匹配优先级"),
      ] {
        let button = NSButton(
          image: PaxIcon.image(symbol) ?? NSImage(), target: self,
          action: #selector(movePackage(_:)))
        button.isBordered = false
        button.identifier = .init(id)
        button.tag = delta
        button.toolTip = text
        button.setAccessibilityLabel(pack.name + " · " + text)
        let next = index + delta
        button.isEnabled =
          store.canEdit && store.packages.indices.contains(next)
          && [.local, .subscription].contains(store.packages[next].kind)
        NSLayoutConstraint.activate([
          button.heightAnchor.constraint(equalToConstant: PaxTheme.Routing.moveHeight),
          button.widthAnchor.constraint(equalToConstant: PaxTheme.Routing.moveSize),
        ])
        arrows.addArrangedSubview(button)
      }
      row.addArrangedSubview(arrows)
      row.hoverActions = arrows
    }
    return row
  }

  func ruleCell(_ id: String, column: Int) -> NSView? {
    guard let pack = store.selectedPackage, let rule = pack.rules.first(where: { $0.id == id })
    else { return nil }
    if column == 0 {
      guard let index = pack.rules.firstIndex(where: { $0.id == id }) else { return nil }
      let number = PaxText.label(String(index + 1), font: PaxTheme.Typography.time, secondary: true)
      number.alignment = .center
      return number
    }
    let column = column - 1
    if column == 0 {
      let label = PaxText.label(
        store.ruleName(rule),
        font: PaxTheme.Typography.label(size: PaxTheme.Table.font, weight: .medium))
      label.lineBreakMode = .byTruncatingTail
      label.toolTip = store.ruleName(rule)
      return label
    }
    if column == 1 {
      let label = PaxText.label(
        (rule.enabled ? "" : "已停用 · ") + rule.method.rawValue,
        font: PaxTheme.Typography.caption(), secondary: true)
      if !rule.enabled {
        let text = NSMutableAttributedString(string: label.stringValue)
        text.addAttribute(
          .foregroundColor, value: PaxTheme.Color.error,
          range: (label.stringValue as NSString).range(of: "已停用"))
        label.attributedStringValue = text
      }
      return label
    }
    if column == 2 && pack.kind == .priority {
      return PaxText.label(
        store.destinationName(rule.destination), font: PaxTheme.Typography.caption())
    }
    guard [.local, .priority].contains(pack.kind) else {
      return PaxText.label(
        store.destinationName(pack.kind == .geo ? rule.destination : pack.destination),
        font: PaxTheme.Typography.caption())
    }
    let row = NSStackView()
    row.spacing = PaxTheme.Spacing.xs
    if pack.kind == .priority {
      let check = NSButton(checkboxWithTitle: "", target: self, action: #selector(enableRule(_:)))
      check.identifier = .init(id)
      check.state = rule.enabled ? .on : .off
      check.setAccessibilityLabel("启用 " + store.ruleName(rule))
      check.isEnabled = store.canEdit
      row.addArrangedSubview(check)
    }
    for (title, action) in [("编辑", #selector(edit(_:))), ("移除", #selector(remove(_:)))] {
      let button = PaxRouteButton(title, style: .quiet, target: self, action: action)
      button.identifier = .init(id)
      button.isEnabled = store.canEdit
      row.addArrangedSubview(button)
    }
    let container = NSStackView(views: [NSView(), row])
    return container
  }
}
