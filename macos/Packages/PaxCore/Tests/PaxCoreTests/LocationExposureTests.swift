import Foundation
import Testing

@testable import PaxClient
@testable import PaxCore

@Suite("定位扫描") struct LocationExposureTests {
  private let me = "AAAA-1111"
  private let at = Date(timeIntervalSince1970: 1_733_011_200)

  private func plist(_ entries: [String: [String: Any]]) -> Data {
    try! PropertyListSerialization.data(fromPropertyList: entries, format: .binary, options: 0)
  }

  private var clients: Data {
    plist([
      "\(me):icom.google.Chrome:": ["BundleId": "com.google.Chrome", "Authorized": true],
      "\(me):icom.notes:": [
        "BundleId": "com.notes", "BundlePath": "/Applications/Notes.app", "Authorized": true,
      ],
      "\(me):icom.denied:": [
        "BundleId": "com.denied", "BundlePath": "/Applications/Denied.app", "Authorized": false,
      ],
      "\(me):icom.unasked:": ["BundleId": "com.unasked", "BundlePath": "/Applications/U.app"],
      "\(me):icom.mini:": ["BundleId": "com.mini", "Authorized": true],
      "\(me):p/System/Library/PrivateFrameworks/X.framework": [
        "BundleId": "com.apple.x", "Authorized": true,
      ],
      "\(me):icom.apple.locationd.bundle-1:": [
        "BundleId": "com.apple.locationd.bundle-1", "Authorized": true,
      ],
      "\(me):icom.svc:": ["BundleId": "com.svc", "Authorized": true, "isSystemService": true],
      "OTHER-2222:icom.other:": ["BundleId": "com.other", "Authorized": true],
      "root:icom.root:": ["BundleId": "com.root", "Authorized": true],
    ])
  }

  private func scan(
    systemEnabled: Bool? = true, clients: Data? = nil
  ) -> LocationExposure {
    LocationExposureScanner.scan(
      clients: clients ?? self.clients, userUID: me, systemEnabled: systemEnabled,
      browsers: [.init(name: "Chrome", bundleID: "com.google.Chrome")])
  }

  private func local(_ location: LocationExposure?) -> NetworkDiagnosticEnvironment {
    NetworkDiagnosticEnvironment(
      timezone: "Asia/Shanghai", languages: ["zh-CN"], region: "zh_CN", at: at, location: location)
  }

  private func rows(_ location: LocationExposure?) -> [EnvironmentDiagnosticPresentation.Row] {
    EnvironmentDiagnosticPresentation.locationRows(nil, local: local(location), waiting: false)
  }

  @Test("授权清单区分允许、拒绝、缺失、他人与系统条目，浏览器不计入应用")
  func clientsParsing() {
    let result = scan()
    #expect(result.apps.map(\.bundleID) == ["com.denied", "com.notes", "com.mini"])
    #expect(result.apps.first { $0.bundleID == "com.denied" }?.allowed == false)
    #expect(result.apps.first { $0.bundleID == "com.notes" }?.name == "Notes")
    #expect(result.apps.first { $0.bundleID == "com.mini" }?.name == "com.mini")
    #expect(result.browsers.first?.authorization == .allowed)
    #expect(result.authorizationsReadable)
  }

  @Test("合并定位行保留系统、应用与浏览器授权并给出风险色")
  func openRows() {
    let result = rows(scan())
    #expect(result.map(\.title) == ["定位"])
    #expect(result.allSatisfy { $0.tone == .difference })
    #expect(result[0].detail.contains("Chrome：未关闭（系统已授权定位）"))
    #expect(result[0].detail.contains("系统设置 › 隐私与安全性 › 定位服务"))
    #expect(result[0].detail.contains("不读取浏览器内的网站定位设置"))
    #expect(result[0].value == "存在位置暴露风险 · 已授权 2 个应用、1 个浏览器")
    #expect(result[0].detail.contains("Notes · /Applications/Notes.app"))
    #expect(result[0].detail.contains("Denied"))
    #expect(result[0].detail.contains("系统服务未列出"))
    #expect(rows(scan(clients: plist([:])))[0].value == "存在潜在风险 · 系统定位已开启，未发现应用或浏览器授权")
  }

  @Test("合并行不因浏览器被拒绝或未授权而隐藏系统开关风险")
  func browserClosed() {
    let denied = plist(["\(me):icom.google.Chrome:": ["Authorized": false]])
    let deniedRow = rows(scan(clients: denied))[0]
    #expect(deniedRow.value == "存在潜在风险 · 系统定位已开启，未发现应用或浏览器授权" && deniedRow.tone == .difference)
    #expect(deniedRow.detail.contains("系统已拒绝"))
    let noAuth = rows(scan(clients: plist([:])))[0]
    #expect(noAuth.value.contains("未发现应用或浏览器授权") && noAuth.detail.contains("系统未授权"))
  }

  @Test("总开关关闭：浏览器与应用写授权不生效，授权清单仍在详情")
  func switchOff() {
    let result = rows(scan(systemEnabled: false))
    #expect(result[0].value == "系统定位已关闭 · 应用与浏览器的定位授权不生效" && result[0].tone == .information)
    #expect(result[0].detail.contains("不生效") && result[0].detail.contains("Notes"))
    #expect(result[0].detail.contains("系统定位已关闭，以下授权不生效"))
    #expect(result[0].detail.contains("Chrome：未关闭（系统已授权定位）"))
  }

  @Test("读取失败为无法读取，不当作已关闭")
  func readFailure() {
    let broken = LocationExposureScanner.scan(
      clients: Data("junk".utf8), userUID: me, systemEnabled: nil,
      browsers: [.init(name: "Chrome", bundleID: "com.google.Chrome")])
    #expect(!broken.authorizationsReadable)
    let result = rows(broken)
    #expect(result.allSatisfy { $0.tone == .pending && $0.value == "位置暴露风险待确认 · 无法读取系统定位开关" })
    let noUser = LocationExposureScanner.scan(
      clients: clients, userUID: nil, systemEnabled: true, browsers: [])
    #expect(!noUser.authorizationsReadable)
    let partial = rows(scan(clients: Data("junk".utf8)))[0]
    #expect(partial.value == "存在潜在风险 · 系统定位已开启，授权清单无法读取" && partial.tone == .difference)
  }

  @Test("旧记录无字段可解码，合并行写旧记录未采集；未检测时为尚未检测")
  func oldRecords() throws {
    let old = Data(
      #"{"timezone":"Asia/Shanghai","languages":["zh-CN"],"region":"zh_CN","at":0}"#.utf8)
    let decoded = try JSONDecoder().decode(NetworkDiagnosticEnvironment.self, from: old)
    #expect(decoded.location == nil)
    let result = rows(nil)
    #expect(result.allSatisfy { $0.value == "旧记录未采集" && $0.tone == .pending })
    let fresh = EnvironmentDiagnosticPresentation.locationRows(nil, local: nil, waiting: false)
    #expect(fresh.count == 1 && fresh[0].value == "尚未检测")
    let waiting = EnvironmentDiagnosticPresentation.locationRows(nil, local: nil, waiting: true)
    #expect(waiting.count == 1 && waiting[0].value == "正在检测…")
    let roundTrip = try JSONDecoder().decode(
      NetworkDiagnosticEnvironment.self,
      from: JSONEncoder().encode(local(scan())))
    #expect(roundTrip.location?.apps.count == 3)
  }

  @Test("已保存的旧 JSON 含 profiles/allowedSites 仍可解码")
  func legacyProfileJSON() throws {
    let json = """
      {"systemEnabled":true,"authorizationsReadable":true,"apps":[],
      "browsers":[{"name":"Chrome","bundleID":"com.google.Chrome","authorization":"allowed",
      "profiles":[{"name":"Default","defaultSetting":"block","allowedSites":["a.com"]}]}]}
      """
    let decoded = try JSONDecoder().decode(LocationExposure.self, from: Data(json.utf8))
    #expect(decoded.browsers.first?.authorization == .allowed)
    #expect(LocationExposure.state(decoded.browsers[0]) == .open)
  }

  @Test("assessedRows 仅含一行定位，排在 IPv6 后")
  func assessed() {
    let env = local(scan())
    let report = EnvironmentDiagnosticReport(
      .with {
        $0.mac = .with { $0.complete = true }
        $0.browser = .with {
          $0.complete = true
          $0.browser = "Chrome"
        }
      })
    let result = EnvironmentDiagnosticPresentation.assessedRows(report, local: env, waiting: false)
    #expect(result.suffix(2).map(\.title) == ["IPv6", "定位"])
    #expect(result.first { $0.title == "定位" }?.detail.hasPrefix("系统定位\n定位服务总开关") == true)
  }
}
