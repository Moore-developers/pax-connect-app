import AppKit
import PaxCore
import UserNotifications

/// The App owns OS authorization and delivery; all threshold decisions come from paxd.
@MainActor final class TrafficAlertNotification: NSObject, UNUserNotificationCenterDelegate {
  private let context: () -> PresentationContext
  private let openRoutes: () -> Void
  init(context: @escaping () -> PresentationContext, openRoutes: @escaping () -> Void) {
    self.context = context
    self.openRoutes = openRoutes
    super.init()
    UNUserNotificationCenter.current().delegate = self
  }
  func authorize() async throws -> Bool {
    let center = UNUserNotificationCenter.current()
    let settings = await center.notificationSettings()
    if settings.authorizationStatus == .notDetermined {
      return try await center.requestAuthorization(options: [.alert, .sound])
    }
    return settings.authorizationStatus == .authorized
      || settings.authorizationStatus == .provisional
  }
  func allowed() async -> Bool {
    let value = await UNUserNotificationCenter.current().notificationSettings()
    return value.authorizationStatus == .authorized || value.authorizationStatus == .provisional
  }
  func permission() async -> Bool? {
    let settings = await UNUserNotificationCenter.current().notificationSettings()
    if settings.authorizationStatus == .notDetermined { return nil }
    return settings.authorizationStatus == .authorized
      || settings.authorizationStatus == .provisional
  }
  func deliver(_ notice: TrafficAlertNotice) async throws {
    guard await allowed() else { throw TrafficAlertDeliveryFailure.denied }
    try Task.checkCancellation()
    let c = context()
    let content = UNMutableNotificationContent()
    content.categoryIdentifier = "pax.traffic-alert"
    content.title = c.text("alerts.notification." + notice.direction)
    let source = notice.sourceKind == "unknown" ? c.text("alerts.unknown_app") : notice.source
    let window = c.text("alerts.window." + String(notice.windowSeconds))
    let route =
      notice.routeID == "direct"
      ? c.text("common.direct")
      : notice.routeName.isEmpty ? c.text("alerts.unknown_route") : notice.routeName
    var body = c.text(
      "alerts.notification.body",
      [
        "source": source, "window": window,
        "amount": TrafficFormatting.bytes(notice.bytes),
        "threshold": TrafficFormatting.bytes(notice.threshold),
        "route": route, "time": TrafficFormatting.time(notice.until),
      ])
    if notice.routeCount > 1 {
      body += c.text("alerts.notification.routes", ["count": String(notice.routeCount - 1)])
    }
    if notice.partial { body += "\n" + c.text("alerts.partial") }
    content.body = body
    content.sound = .default
    try await UNUserNotificationCenter.current().add(
      .init(identifier: "traffic-alert-" + notice.id, content: content, trigger: nil))
  }
  nonisolated func userNotificationCenter(
    _ center: UNUserNotificationCenter,
    willPresent notification: UNNotification,
    withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
  ) {
    completionHandler([.banner, .list, .sound])
  }
  nonisolated func userNotificationCenter(
    _ center: UNUserNotificationCenter,
    didReceive response: UNNotificationResponse
  ) async {
    guard response.notification.request.content.categoryIdentifier == "pax.traffic-alert" else {
      return
    }
    await MainActor.run { openRoutes() }
  }
}

extension AppDelegate {
  func makeTrafficAlertsStore() -> TrafficAlertsStore {
    let connection = commandConnection
    return TrafficAlertsStore(
      read: { try await connection.trafficAlerts() },
      write: { try await connection.saveTrafficAlertSettings($0) },
      authorize: { [weak self] in try await self?.trafficNotifications.authorize() ?? false },
      deliver: { [weak self] notice in
        guard let self else { throw CancellationError() }
        try await self.trafficNotifications.deliver(notice)
      }, deliveredIDs: UserDefaults.standard.stringArray(forKey: "traffic-alert-delivered") ?? [],
      persist: { UserDefaults.standard.set($0, forKey: "traffic-alert-delivered") })
  }
}
