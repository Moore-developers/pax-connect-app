import AppKit
import PaxCore
import PaxUI

/// System includes non-user-app connections as well as product events.
func logSystemCellText(_ record: LogRecord, column: Int, context: PresentationContext = .chinese)
  -> String
{
  switch column {
  case 0:
    return record.lastAt.map(TrafficFormatting.logDate) ?? context.text("session_limits.7450cc5b16")
  case 1:
    return record.flow.map {
      FlowPresentation.application($0, context: context) + " → "
        + FlowPresentation.target($0, context: context)
    }
      ?? record.systemEvent.map { ProductTextPresentation.eventName($0, context: context) }
      ?? ProductTextPresentation.recordTitle(record, context: context)
  default:
    return record.flow.map { FlowPresentation.actualConnection($0, context: context) }
      ?? (!record.operationFrom.isEmpty && !record.operationTo.isEmpty
        ? record.operationFrom + " → " + record.operationTo : "—")
  }
}

extension LogsWorkbenchViewController {
  func cell(_ id: String, column: Int) -> NSView? {
    if let record = store.visibleRecords.first(where: { $0.id == id }) {
      let flow = record.flow
      let context = interfaceCopy.context
      if column == 0 {
        let date = record.lastAt
        let full = date.map(TrafficFormatting.logDate) ?? context.text("session_limits.7450cc5b16")
        let label = compactLabel(
          (context.language == .english ? date.map(TrafficFormatting.logDateWithoutYear) : nil)
            ?? full)
        label.toolTip = full
        return label
      }
      if store.dimension == .system {
        return column == 2
          ? statusLabel(record)
          : compactLabel(logSystemCellText(record, column: column, context: interfaceCopy.context))
      }

      switch column {
      case 1:
        return compactLabel(store.recordIdentity(record, context: interfaceCopy.context))
      case 2: return statusLabel(record)
      case 3:
        return compactLabel(
          flow.map { FlowPresentation.actualConnection($0, context: interfaceCopy.context) } ?? "—")
      case 4:
        let text = TrafficFormatting.milliseconds(flow.flatMap(TrafficFormatting.responseLatency))
        let label = compactLabel(
          text
            + (flow?.purpose == "dns" && text != "—"
              ? self.interfaceCopy.text("interface_detail.21bf143e09") : ""))
        label.toolTip =
          flow.map { FlowPresentation.latencyReason($0, context: interfaceCopy.context) }
          ?? self.interfaceCopy.text("interface_detail.2746d99580")
        return label
      default:
        let metrics = record.metrics
        guard ["complete", "partial"].contains(metrics.bytesState) else {
          let row = NSStackView(views: [compactLabel("—")])
          row.alignment = .centerY
          return row
        }
        let upload = TrafficFormatting.bytes(metrics.upload)
        let download = TrafficFormatting.bytes(metrics.download)
        return PaxTrafficCells.transfer(
          uploadText: upload, downloadText: download,
          upload: Double(metrics.upload), download: Double(metrics.download),
          note: TrafficFormatting.connectionMetricsNote(metrics, context: context),
          accessibilityReadout: context.text(
            "traffic.transfer_readout", ["up": upload, "down": download]),
          stacksWhenNarrow: context.language == .english)
      }
    }
    guard let event = store.events.first(where: { $0.id == id }) else { return nil }
    if column == 0 { return compactLabel(event.time) }
    switch column {
    case 1:
      return compactLabel(
        store.eventIdentity(event, context: interfaceCopy.context))
    case 2: return PaxText.label(event.title, wrapping: true)
    case 3: return PaxText.label(event.route, secondary: true)
    default: return PaxText.label("—", secondary: true)
    }
  }
  func transferCellKey(_ id: String) -> [String] {
    guard let metrics = store.visibleRecords.first(where: { $0.id == id })?.metrics else {
      return []
    }
    return [
      metrics.bytesState, String(metrics.upload), String(metrics.download),
      TrafficFormatting.connectionMetricsNote(metrics, context: self.interfaceCopy.context),
    ]
  }
  private func statusLabel(_ record: LogRecord) -> NSTextField {
    let label = compactLabel(
      FlowPresentation.recordedStatus(
        record.flow,
        fallback: ProductTextPresentation.recordTitle(record, context: interfaceCopy.context),
        context: interfaceCopy.context))
    let flow = record.flow
    label.textColor =
      flow?.direction == "blocked" || flow?.failure == "rule_rejected" || flow?.stateUnknown == true
      ? PaxTheme.Logs.warningText
      : record.level == .error
        ? PaxTheme.Logs.errorText
        : record.level == .warning ? PaxTheme.Logs.warningText : PaxTheme.Color.text
    return label
  }
  private func compactLabel(_ value: String) -> NSTextField {
    let label = PaxText.label(value, font: PaxTheme.Typography.caption())
    label.lineBreakMode = .byTruncatingTail
    label.maximumNumberOfLines = 1
    label.toolTip = value
    return label
  }
}
