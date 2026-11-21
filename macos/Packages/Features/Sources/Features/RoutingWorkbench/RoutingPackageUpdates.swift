import AppKit
import PaxCore
import PaxUI

@MainActor
extension RoutingPackagesController {
  func renderUpdates(_ pack: RoutingWorkbenchStore.RulePackage) {
    for item in updateArea.arrangedSubviews {
      updateArea.removeArrangedSubview(item)
      item.removeFromSuperview()
    }
    guard pack.kind.canUpdate else {
      updateArea.isHidden = true
      return
    }
    updateArea.isHidden = false
    let expand = NSButton(
      title: self.interfaceCopy.text("interface_detail.6f5a1a563f"), target: self,
      action: #selector(toggleUpdates))
    expand.setButtonType(.toggle)
    expand.imagePosition = .imageLeading
    expand.image = PaxIcon.image(showsUpdates ? "chevron.down" : "chevron.right")
    expand.alternateImage = expand.image
    expand.isBordered = false
    expand.state = showsUpdates ? .on : .off
    let header = NSStackView(views: [expand])
    header.spacing = PaxTheme.Spacing.tight
    header.heightAnchor.constraint(equalToConstant: PaxTheme.Control.compactHeight).isActive = true
    updateArea.addArrangedSubview(header)
    if showsUpdates {
      let source = PaxText.label(
        RoutingGeoPresentation.source(pack, context: interfaceCopy.context),
        font: PaxTheme.Typography.time, secondary: true,
        wrapping: true)
      source.heightAnchor.constraint(
        greaterThanOrEqualToConstant: source.intrinsicContentSize.height
      ).isActive = true
      updateArea.addArrangedSubview(source)
      if store.telemetry != nil && pack.kind == .geo {
        updateArea.addArrangedSubview(
          PaxText.label(
            RoutingGeoPresentation.sourceExplanation(context: interfaceCopy.context),
            secondary: true,
            wrapping: true))
        return
      }
      let version = PaxText.label(
        self.interfaceCopy.text("interface_detail.8432b4e50b") + pack.version,
        font: PaxTheme.Typography.time, secondary: true)
      version.heightAnchor.constraint(equalToConstant: version.intrinsicContentSize.height)
        .isActive = true
      updateArea.addArrangedSubview(version)
      let automatic = NSButton(
        checkboxWithTitle: self.interfaceCopy.text("interface_detail.80faccd7df"), target: self,
        action: #selector(automaticChanged(_:)))
      automatic.state = pack.automatic ? .on : .off
      automatic.isEnabled = store.canEdit && !pack.updating
      let rollback = PaxRouteButton(
        self.interfaceCopy.text("interface_detail.e700bc424c"), target: self,
        action: #selector(rollback))
      rollback.isEnabled = store.canEdit && pack.previousVersion != nil && !pack.updating
      let row = NSStackView(views: [automatic, rollback])
      row.spacing = PaxTheme.Spacing.tight
      row.heightAnchor.constraint(equalToConstant: PaxTheme.Routes.controlHeight).isActive = true
      updateArea.addArrangedSubview(row)
    }
    if !pack.updateMessage.isEmpty {
      updateArea.addArrangedSubview(
        PaxText.label(
          ProductTextPresentation.text(
            pack.updateText, original: pack.updateMessage, context: interfaceCopy.context),
          secondary: true, wrapping: true))
    }
    for item in updateArea.arrangedSubviews {
      item.widthAnchor.constraint(equalTo: updateArea.widthAnchor).isActive = true
    }
  }
  @objc func toggleUpdates() {
    showsUpdates.toggle()
    render()
  }
  @objc func automaticChanged(_ sender: NSButton) {
    store.perform(.automatic(store.selectedPackageID, sender.state == .on))
  }
  @objc func rollback() { store.perform(.rollback(store.selectedPackageID)) }
}
