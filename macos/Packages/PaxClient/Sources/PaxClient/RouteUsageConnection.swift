import Foundation
import GRPCCore
import PaxProto

extension DaemonConnection {
  public func routeUsage(_ id: String, cursor: String = "", from: Date? = nil, window: String = "")
    async throws
    -> RouteUsagePage
  {
    try await withAuthenticatedClient { client in
      let service = Pax_V1_PaxService.Client(wrapping: client)
      var options = CallOptions.defaults
      options.timeout = .seconds(5)
      let v = try await service.queryRouteUsage(
        .with {
          $0.routeID = id
          $0.window = window
          $0.cursor = cursor
          $0.limit = 100
          if let from { $0.from.seconds = Int64(from.timeIntervalSince1970) }
        }, options: options)
      return RouteUsagePage(v)
    }
  }

}

extension RouteUsagePage {
  init(_ v: Pax_V1_RouteUsageResponse) {
    self.init(
      items: v.items.map {
        var bucket = RouteUsageBucket(
          at: $0.at.date, upload: $0.upload, download: $0.download, gap: $0.gap)
        bucket.seconds = $0.seconds
        bucket.partial = $0.partial
        bucket.connections = $0.connections
        return bucket
      }, nextCursor: v.nextCursor, partial: v.partial,
      todayUpload: v.todayUpload, todayDownload: v.todayDownload, connections: v.connections,
      sampledAt: v.hasSampledAt ? v.sampledAt.date : nil,
      error: v.hasError ? ProductError(proto: v.error) : nil)
    routes = v.routes.map {
      .init(
        routeID: $0.routeID, todayUpload: $0.todayUpload,
        todayDownload: $0.todayDownload, hourUpload: $0.hourUpload, hourDownload: $0.hourDownload)
    }
    if v.hasAlertProgress && v.alertProgress.hasSettings {
      let progress = v.alertProgress
      alertProgress = .init(
        monitorState: progress.monitorState, settings: TrafficAlertSettings(progress.settings),
        windows: progress.windows.compactMap { sample in
          guard sample.hasUntil else { return nil }
          return .init(
            seconds: sample.seconds, upload: sample.upload, download: sample.download,
            partial: sample.partial, until: sample.until.date)
        })
    }
    hourFrom = v.hasHourFrom ? v.hourFrom.date : nil
    let rank: (Pax_V1_RouteUsageRank) -> RouteUsageRank = {
      .init(id: $0.id, name: $0.name, upload: $0.upload, download: $0.download)
    }
    applications = v.applications.map(rank)
    domains = v.domains.map(rank)
    ips = v.ips.map(rank)
    coverageStart = v.hasCoverageStart ? v.coverageStart.date : nil
    upload = v.upload
    download = v.download
    from = v.hasFrom ? v.from.date : nil
    until = v.hasUntil ? v.until.date : nil
    basis = v.basis
    windowConnections = v.windowConnections
  }
}
