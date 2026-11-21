import AppKit
import PaxCore
import PaxUI
import UniformTypeIdentifiers

/// Edge measurements remain distinct from full draft validation.
@MainActor enum CFRecordPresentation {
  static func row(
    _ record: CFRecord, currentNetwork: String = "", selected: String, pendingColumn: Int? = nil,
    context: PresentationContext = .chinese
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
      } ?? context.text("route_readouts.c9c37a1eba")
    func caption(_ sample: CFSample) -> String {
      let time =
        (record.network != currentNetwork && sample.value > 0
          ? context.text("route_readouts.bc849d9979") : "")
        + MeasurementTimestamp.text(sample.measuredAt)
      return sample.errorCode.isEmpty
        ? time
        : time + context.text("route_readouts.bb62f62a2e")
          + MeasurementTimestamp.text(sample.attemptedAt)
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
        context.text("route_readouts.fe54b3e26d")
        + ProductErrorPresentation.message(
          ProductError(code: record.validation.errorCode), context: context)
        + " · " + MeasurementTimestamp.text(record.validation.attemptedAt)
    }
    if let pendingColumn, (1...3).contains(pendingColumn) {
      let previous = values[pendingColumn]
      values[pendingColumn] =
        pendingColumn == 3
        ? context.text("route_readouts.d302f90b09") : context.text("route_readouts.105a9082c3")
      captions[pendingColumn] =
        previous == "—" || location == nil
        ? context.text("route_readouts.029954b7a5")
        : context.text("route_readouts.4ce1601064") + previous
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
