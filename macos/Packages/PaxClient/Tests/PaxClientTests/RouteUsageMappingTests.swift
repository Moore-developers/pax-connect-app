import PaxProto
import Testing

@testable import PaxClient

extension RouteUsageMappingTests {
  @Test func routeAlertProgressPreservesSampleAndCoverage() {
    var settings = TrafficAlertSettings()
    settings.enabled = true
    settings.mediumSeconds = 7200
    let page = RouteUsagePage(
      .with {
        $0.alertProgress.monitorState = "partial"
        $0.alertProgress.settings = settings.proto
        $0.alertProgress.windows = [
          .with {
            $0.seconds = 7200
            $0.until.seconds = 12345
            $0.partial = true
            $0.upload = 500
            $0.download = 700
          }
        ]
      })
    #expect(page.alertProgress?.settings == settings)
    #expect(page.alertProgress?.windows.first?.partial == true)
    #expect(page.alertProgress?.windows.first?.download == 700)
    #expect(page.alertProgress?.windows.first?.until.timeIntervalSince1970 == 12345)
    #expect(RouteUsagePage(.init()).alertProgress == nil)
  }
  @Test func batchAndRanksPreserveBytesAndIdentity() {
    let value = Pax_V1_RouteUsageResponse.with {
      $0.routes = [
        .with {
          $0.routeID = "a"
          $0.todayUpload = 42
          $0.hourDownload = 1_100_000_000
        }
      ]
      $0.hourFrom.seconds = 100
      $0.applications = [
        .with {
          $0.id = "app:one"
          $0.name = "Downloader"
          $0.download = 1_100_000_000
        }
      ]
      $0.domains = [
        .with {
          $0.id = "jjdsn.com"
          $0.name = "jjdsn.com"
          $0.upload = 42
        }
      ]
      $0.ips = [
        .with {
          $0.id = "203.0.113.1"
          $0.name = "203.0.113.1"
          $0.download = 7
        }
      ]
    }
    let page = RouteUsagePage(value)
    #expect(page.routes.first?.routeID == "a")
    #expect(page.routes.first?.todayUpload == 42)
    #expect(page.routes.first?.hourDownload == 1_100_000_000)
    #expect(page.hourFrom?.timeIntervalSince1970 == 100)
    #expect(page.applications.first?.download == 1_100_000_000)
    #expect(page.domains.first?.upload == 42)
    #expect(page.ips.first?.download == 7)
  }
}
