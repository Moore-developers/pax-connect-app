import AppKit
import PaxCore
import PaxUI
import UniformTypeIdentifiers

/// Edge measurements remain distinct from full draft validation.
@MainActor enum CFRecordPresentation {
  static func row(
    _ record: CFRecord, currentNetwork: String = "", selected: String, pendingColumn: Int? = nil
  )
    -> PaxCFRecordTable.Row
  {
    let location = record.location
    let region =
      location.flatMap { value -> String? in
        guard value.errorCode.isEmpty else { return nil }
        return ExitLocationPresentation.region(
          .init(
            ip: record.ip, countryCode: value.countryCode, country: value.country,
            region: value.region, city: value.city, at: value.at ?? Date()))
      } ?? "未获取"
    func caption(_ sample: CFSample) -> String {
      let time =
        (record.network != currentNetwork && sample.value > 0 ? "原网络 · " : "")
        + MeasurementTimestamp.text(sample.measuredAt)
      return sample.errorCode.isEmpty
        ? time : time + " · 最近失败 " + MeasurementTimestamp.text(sample.attemptedAt)
    }
    var values = [
      record.ip,
      MeasurementFormatting.latency(record.tcp.value > 0 ? record.tcp.value : nil)?.text ?? "—",
      MeasurementFormatting.speed(
        bytesPerSecond: record.download.value > 0 ? record.download.value : nil)?.text ?? "—",
      region,
      "",
    ]
    var captions = [
      record.prefix, caption(record.tcp), caption(record.download),
      location?.source ?? "",
      "",
    ]
    let validationFailed =
      !currentNetwork.isEmpty && record.network == currentNetwork && record.validationMatches
      && record.validation.attemptedAt != nil && !record.validation.errorCode.isEmpty
    if validationFailed {
      captions[0] =
        "本线路验证失败 · "
        + ProductErrorPresentation.message(ProductError(code: record.validation.errorCode))
        + " · " + MeasurementTimestamp.text(record.validation.attemptedAt)
    }
    if let pendingColumn, (1...3).contains(pendingColumn) {
      let previous = values[pendingColumn]
      values[pendingColumn] = pendingColumn == 3 ? "查询中…" : "测试中…"
      captions[pendingColumn] = previous == "—" || previous == "未获取" ? "等待本次结果" : "上次：" + previous
    }
    return .init(
      id: record.ip, values: values, captions: captions,
      numbers: [
        1: record.tcp.value > 0 ? record.tcp.value : .infinity, 2: record.download.value,
        3: record.validation.value,
      ], failed: validationFailed)
  }

  static func export(
    _ store: CFWorkspaceStore, in window: NSWindow?, report: @escaping (Error) -> Void
  ) {
    guard let window else { return }
    let panel = NSSavePanel()
    panel.nameFieldStringValue = "cf-records.json"
    panel.allowedContentTypes = [.json, .commaSeparatedText]
    panel.beginSheetModal(for: window) { response in
      guard response == .OK, let url = panel.url else { return }
      do {
        let data =
          url.pathExtension.lowercased() == "csv"
          ? Data(store.exportCSV().utf8) : try store.exportJSON()
        try data.write(to: url, options: .atomic)
      } catch { report(error) }
    }
  }
}
