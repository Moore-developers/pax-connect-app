import AppKit
import PaxCore

extension NetworkDiagnosticView {
  func sourcesContent() -> NSView {
    let report = intelligenceReport
    let sources = P.sourceRows(report, context: context)
    let assessment = P.sourceListOverview(sources, context: context)
    let summary = topicSummary(
      assessment.text, tone: assessment.tone,
      metadata: routeSummaryMetadata(report, sourceData: true))
    guard !sources.isEmpty else { return summary }
    var rows: [NSView] = []
    for source in sources {
      let verdictKey: String
      if source.unavailable {
        verdictKey = "browser.check_unavailable"
      } else {
        switch source.tone {
        case .difference: verdictKey = "diagnostic.overview.disagreement"
        case .pending: verdictKey = "diagnostic.overview.partial"
        default: verdictKey = "browser.check_observed"
        }
      }
      rows.append(
        diagnosticRow(
          title: source.title, subtitle: source.value, symbol: "globe", detail: source.detail,
          tone: source.tone, verdict: context.text(verdictKey), basis: source.value))
    }
    let list = diagnosticTable(rows)
    list.setAccessibilityIdentifier("diagnostic-sources-table")
    return vertical([summary, list])
  }
}
