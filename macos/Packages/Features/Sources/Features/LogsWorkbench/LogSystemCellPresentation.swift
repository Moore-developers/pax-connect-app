import AppKit
import PaxCore
import PaxUI

/// System includes non-user-app connections as well as product events.
func logSystemCellText(_ record: LogRecord, column: Int) -> String {
  switch column {
  case 0: return record.lastAt.map(TrafficFormatting.logDate) ?? "未记录"
  case 1:
    return record.flow.map {
      FlowPresentation.application($0) + " → " + FlowPresentation.target($0)
    }
      ?? String(record.title.split(separator: "：").first ?? "系统事件")
  default:
    return record.flow.map(FlowPresentation.actualConnection)
      ?? (!record.operationFrom.isEmpty && !record.operationTo.isEmpty
        ? record.operationFrom + " → " + record.operationTo : "—")
  }
}

extension LogsWorkbenchViewController {
  func cell(_ id: String, column: Int) -> NSView? {
    if let record = store.visibleRecords.first(where: { $0.id == id }) {
      let flow = record.flow
      if store.dimension == .system {
        return column == 2
          ? statusLabel(record) : compactLabel(logSystemCellText(record, column: column))
      }

      switch column {
      case 0: return compactLabel(record.lastAt.map(TrafficFormatting.logDate) ?? "未记录")
      case 1:
        return compactLabel(store.recordIdentity(record))
      case 2: return statusLabel(record)
      case 3:
        return compactLabel(flow.map(FlowPresentation.actualConnection) ?? "—")
      case 4:
        let text = TrafficFormatting.milliseconds(flow.flatMap(TrafficFormatting.responseLatency))
        let label = compactLabel(text + (flow?.purpose == "dns" && text != "—" ? " · 解析" : ""))
        label.toolTip = flow.map(FlowPresentation.latencyReason) ?? "不适用"
        return label
      default:
        let metrics = record.metrics
        guard ["complete", "partial"].contains(metrics.bytesState) else {
          let row = NSStackView(views: [compactLabel("—")])
          row.alignment = .centerY
          return row
        }
        return PaxTrafficCells.transfer(
          uploadText: TrafficFormatting.bytes(metrics.upload),
          downloadText: TrafficFormatting.bytes(metrics.download),
          upload: Double(metrics.upload), download: Double(metrics.download),
          note: TrafficFormatting.connectionMetricsNote(metrics))
      }
    }
    guard let event = store.events.first(where: { $0.id == id }) else { return nil }
    if column == 0 { return compactLabel(event.time) }
    switch column {
    case 1:
      return compactLabel(
        store.eventIdentity(event))
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
      TrafficFormatting.connectionMetricsNote(metrics),
    ]
  }
  private func statusLabel(_ record: LogRecord) -> NSTextField {
    let label = compactLabel(FlowPresentation.recordedStatus(record.flow, fallback: record.title))
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
