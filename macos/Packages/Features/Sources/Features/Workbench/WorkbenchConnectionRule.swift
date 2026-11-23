import AppKit
import PaxCore
import PaxUI

extension WorkbenchTrafficDetailController {
  func openConnectionRule(_ group: TrafficConnectionGroup) {
    guard group.kind != "unknown", let store = ruleStore else { return }
    ruleTask?.cancel()
    ruleTask = Task { [weak self] in
      do {
        if let telemetry = store.telemetry {
          let library = try await telemetry.routingLibrary()
          guard !Task.isCancelled else { return }
          store.applyLibrary(library)
        }
        guard let self, !Task.isCancelled,
          let priority = store.packages.first(where: { $0.kind == .priority })
        else { return }
        let method: RoutingWorkbenchStore.Method = group.kind == "ip" ? .ip : .domain
        let existing = priority.rules.first { $0.method == method && $0.target == group.target }
        let editor = RoutingRuleEditor(
          store: store, packageID: priority.id,
          ruleID: existing?.id, prefilled: (method, group.target))
        self.groupsView?.ruleEditorOpen = true
        editor.onDismissed = { [weak self] in self?.groupsView?.ruleEditorOpen = false }
        editor.onConnectionRuleSaved = { [weak self, weak editor] in
          guard let self else { return }
          var owner = self.presentingViewController
          while let current = owner, !(current is WorkbenchShellViewController) {
            owner = current.parent ?? current.presentingViewController
          }
          let shell = owner as? WorkbenchShellViewController
          editor?.onDismissed = { [weak self, weak shell] in
            self?.onDismissed = { [weak shell] in shell?.showRules(packageID: priority.id) }
            self?.close()
          }
        }
        self.presentWorkbenchModal(editor)
      } catch {
        guard let self, !Task.isCancelled else { return }
        self.pane.status.stringValue = ProductErrorPresentation.display(
          error, context: self.interfaceCopy.context)
      }
    }
  }
}
