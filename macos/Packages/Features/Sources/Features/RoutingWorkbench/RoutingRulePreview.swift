import AppKit
import PaxCore
import PaxUI

extension RoutingRuleEditor {
  func updateCandidateFooter() {
    let footer = pane.status
    footer.isHidden = false
    footer.textColor = PaxTheme.Color.textSecondary
    guard let pack = store.package(draft.packageID) else {
      footer.stringValue = self.interfaceCopy.text("interface_detail.b3dcfffa77")
      return
    }
    if destination != nil && destination?.selection == nil {
      footer.stringValue = self.interfaceCopy.text("interface_detail.13fa119f20")
      return
    }
    if draft.target.isEmpty || (kind == .application && !selectedApplicationAvailable) {
      footer.stringValue =
        draft.target.isEmpty
        ? self.interfaceCopy.text("interface_detail.da590a8fe3") + kind.title + "。"
        : self.interfaceCopy.text("interface_detail.cf35abff7e")
      return
    }
    do {
      _ = try store.ruleProjection(draft, in: pack)
    } catch {
      footer.stringValue =
        (error as? RoutingWorkbenchStore.EditError)?.message
        ?? self.interfaceCopy.text("interface_detail.9f0d1904d5")
      return
    }
    let target: String
    if kind == .process {
      target = processName(for: draft.target)
    } else if draft.importDomains {
      target =
        self.interfaceCopy.text(
          "interface_detail.8a066be426", ["arg0": String(describing: draft.selectedDomains.count)])
        + (draft.domainMethod == .domainSuffix
          ? self.interfaceCopy.text("interface_detail.d970428baf")
          : self.interfaceCopy.text("interface_detail.3d1aa10e58"))
    } else {
      target = store.applications.first { $0.id == draft.target }?.name ?? draft.target
    }
    let dest = pack.kind == .priority ? draft.destination : pack.destination
    let headline = target + " → " + store.destinationName(dest, context: self.interfaceCopy.context)
    let summary = NSMutableAttributedString(
      string: headline,
      attributes: [
        .font: PaxTheme.Typography.label(weight: .semibold), .foregroundColor: PaxTheme.Color.text,
      ])
    let note =
      pack.kind == .priority
      ? self.interfaceCopy.text("interface_detail.a8736e10e8")
      : self.interfaceCopy.text("interface_detail.67632761c1") + pack.name
        + self.interfaceCopy.text("interface_detail.5c69499bcc")
    summary.append(
      NSAttributedString(
        string: "\n" + note,
        attributes: [
          .font: PaxTheme.Typography.caption(), .foregroundColor: PaxTheme.Color.textSecondary,
        ]))
    footer.attributedStringValue = summary
  }
}
