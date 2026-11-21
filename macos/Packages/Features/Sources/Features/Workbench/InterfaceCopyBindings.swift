import AppKit
import PaxCore
import PaxUI

/// Page-owned bindings update only explicitly registered product text, never user input.
@MainActor final class InterfaceCopyBindings {
  private static let fallback = PresentationContext.chinese
  private(set) var context: PresentationContext = fallback
  private struct Binding {
    weak var target: AnyObject?
    let update: @MainActor (PresentationContext) -> Void
  }
  private var bindings: [Binding] = []
  var onChange: (@MainActor () -> Void)?

  func apply(_ context: PresentationContext) {
    self.context = context
    bindings.removeAll { $0.target == nil }
    for binding in bindings { binding.update(context) }
    onChange?()
  }
  func text(_ key: String, _ parameters: [String: String] = [:], count: Int? = nil) -> String {
    context.text(key, parameters, count: count)
  }
  @discardableResult func bind<T: AnyObject>(
    _ target: T, key: String, parameters: [String: String] = [:],
    set: @escaping @MainActor (T, String) -> Void
  ) -> T {
    let binding: @MainActor (PresentationContext) -> Void = { [weak target] context in
      if let target { set(target, context.text(key, parameters)) }
    }
    bindings.removeAll { $0.target == nil }
    bindings.append(Binding(target: target, update: binding))
    binding(context)
    return target
  }
  @discardableResult func bind<T: AnyObject>(
    _ target: T, update: @escaping @MainActor (T, PresentationContext) -> Void
  ) -> T {
    let apply: @MainActor (PresentationContext) -> Void = { [weak target] context in
      if let target { update(target, context) }
    }
    bindings.removeAll { $0.target == nil }
    bindings.append(Binding(target: target, update: apply))
    apply(context)
    return target
  }
  func label(
    _ key: String, font: NSFont = PaxTheme.Typography.label(), secondary: Bool = false,
    wrapping: Bool = false
  ) -> NSTextField {
    bind(PaxText.label("", font: font, secondary: secondary, wrapping: wrapping), key: key) {
      $0.stringValue = $1
    }
  }
  func routeLabel(
    _ key: String, font: NSFont = PaxTheme.Typography.label(), secondary: Bool = false,
    wrapping: Bool = false
  ) -> NSTextField {
    bind(PaxRouteText.label("", font: font, secondary: secondary, wrapping: wrapping), key: key) {
      $0.stringValue = $1
    }
  }
  @discardableResult func button<T: NSButton>(_ button: T, key: String) -> T {
    bind(button, key: key) {
      $0.title = $1
      $0.setAccessibilityLabel($1)
    }
  }
  @discardableResult func group(_ group: PaxPreferenceGroup, key: String) -> PaxPreferenceGroup {
    bind(group, key: key) { $0.apply(title: $1) }
  }
  func preference(
    title: String, detail: String, control: NSView
  ) -> PaxPreferenceRow {
    let row = PaxPreferenceRow(title: "", detail: "", control: control, style: .compact)
    bind(row, key: title) { [weak self] row, title in
      row.apply(title: title, detail: self?.text(detail) ?? "")
    }
    return row
  }
  func picker(_ picker: NSPopUpButton, keys: [String]) {
    let binding: @MainActor (PresentationContext) -> Void = { [weak picker] context in
      guard let picker else { return }
      let selected = picker.indexOfSelectedItem
      if picker.numberOfItems != keys.count {
        picker.removeAllItems()
        picker.addItems(withTitles: keys.map { context.text($0) })
      } else {
        for (index, key) in keys.enumerated() { picker.item(at: index)?.title = context.text(key) }
      }
      if selected >= 0 { picker.selectItem(at: selected) }
    }
    bindings.removeAll { $0.target == nil }
    bindings.append(Binding(target: picker, update: binding))
    binding(context)
  }
}

@MainActor protocol InterfaceLanguageConsumer: AnyObject {
  var interfaceCopy: InterfaceCopyBindings { get }
}

extension NSViewController {
  /// Traverses existing page controllers only. Presented editors keep their creation snapshot.
  func applyInterfaceLanguage(_ context: PresentationContext) {
    if self is WorkbenchModalController { return }
    (self as? any InterfaceLanguageConsumer)?.interfaceCopy.apply(context)
    for child in children { child.applyInterfaceLanguage(context) }
  }
}
