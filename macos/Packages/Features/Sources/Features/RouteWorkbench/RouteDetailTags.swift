import AppKit
import PaxCore
import PaxUI

extension RouteDetailController {
  func tags() -> NSView {
    let row = NSStackView()
    row.spacing = PaxTheme.Spacing.xs
    if let n = store.node(id) {
      row.addArrangedSubview(
        PaxFeatureTag(ProxyRoutePresentation.protocolLabel(n), style: .protocolLabel))
    }
    let p = store.info(id, context: self.interfaceCopy.context)
    if p.cfIP != nil {
      row.addArrangedSubview(PaxFeatureTag("CF", tone: .blue))
    }
    if p.chain != nil {
      row.addArrangedSubview(
        PaxFeatureTag(self.interfaceCopy.text("action.routes.chain_proxy"), tone: .purple))
    }
    if store.isCurrent(id) {
      row.addArrangedSubview(
        PaxFeatureTag(
          self.interfaceCopy.text("dashboard.current_route"), symbol: "checkmark", tone: .blue))
    }
    return row
  }
}
