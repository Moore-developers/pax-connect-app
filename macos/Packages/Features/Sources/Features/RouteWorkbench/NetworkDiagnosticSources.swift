import AppKit
import PaxCore
import PaxUI

extension NetworkDiagnosticView {
  func sourcesContent() -> NSView {
    let filter = action(
      store.onlyDifferences
        ? context.text("native_diagnostic.9f357adb61")
        : context.text("native_diagnostic.5f36aced9d")
    ) { [weak self] in
      self?.store.onlyDifferences.toggle()
    }
    filter.setAccessibilityIdentifier("network-source-filter")
    var rows: [NSView] = []
    for source in P.sourceRows(
      intelligenceReport, onlyDifferences: store.onlyDifferences, context: context)
    {
      if !rows.isEmpty { rows.append(divider()) }
      rows.append(
        diagnosticRow(
          title: source.title, subtitle: source.value, symbol: "globe", detail: source.detail,
          tone: source.tone))
    }
    let list = vertical(rows, spacing: 0)
    list.setAccessibilityIdentifier("diagnostic-sources-table")
    let views: [NSView] = [
      horizontal([
        heading(context.text("native_diagnostic.9608f6ef4a"), cardTitle: true), NSView(), filter,
      ]),
      text(
        context.text("native_diagnostic.8870892b72")
          + (report?.targetRouteName ?? context.text("native_diagnostic.4d2966c438"))
          + context.text("native_diagnostic.803d722e2f")
          + MeasurementTimestamp.text(report?.at)
          + (store.stale ? context.text("native_diagnostic.68b60191d6") : ""), secondary: true),
      assessmentText(P.sourceListOverview(intelligenceReport, context: context)), list,
      text(P.acquisitionSummary(intelligenceReport, context: context), secondary: true),
      text(
        rows.isEmpty
          ? context.text("native_diagnostic.19e7b8f8e3")
          : context.text("native_diagnostic.a136915ce9"), secondary: true),
      text(
        context.text("native_diagnostic.4e31faa72a")
          + MeasurementTimestamp.text(intelligenceReport?.at)
          + (intelligenceReport?.at != report?.at
            ? context.text("native_diagnostic.08b1ae01f0")
              + P.problem(report?.providerError ?? "", context: context) : ""),
        secondary: true),
    ]
    return vertical(views)
  }

}
