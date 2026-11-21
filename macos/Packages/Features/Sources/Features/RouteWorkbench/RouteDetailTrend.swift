import AppKit
import PaxCore
import PaxUI

extension RouteDetailController {
  func trend(_ result: RouteWorkbenchStore.Measurement, color: NSColor, height: CGFloat)
    -> NSView
  {
    let view = PaxTrendView()
    view.presentation = height == PaxTheme.Routes.miniHeight ? .sparkline : .standard
    view.color = color
    view.samples = result.samples.enumerated().map {
      .init(
        value: $0.element,
        label: self.interfaceCopy.text(
          "interface_detail.79650ba1fa",
          [
            "arg0": String(describing: $0.offset + 1),
            "arg1": String(describing: MeasurementFormatting.latency($0.element)?.text ?? "—"),
          ]))
    }
    view.tooltipCoordinator = tooltip
    view.heightAnchor.constraint(equalToConstant: height).isActive = true
    return view
  }
}
