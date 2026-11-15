import Foundation

extension EnvironmentDiagnosticPresentation {
  public static let locationTitle = "定位"

  struct LocationReading {
    let value: String
    let detail: String
    /// States, counts and browser names only: no app name or path.
    let reportDetail: String
    let tone: Tone
  }

  private static let systemPath = "系统设置 › 隐私与安全性 › 定位服务"
  private static let notScanned = "未扫描：系统服务、Safari 站点设置（需完全磁盘访问）、企业策略。"
  private static let offNote = "系统定位已关闭，以下授权不生效"

  /// One combined row after IPv6. `local` nil with a report means an old record.
  static func locationRows(
    _ report: EnvironmentDiagnosticReport?, local: NetworkDiagnosticEnvironment?, waiting: Bool
  ) -> [Row] {
    let reading: LocationReading
    if local == nil && report == nil {
      let text = waiting ? "正在检测…" : "尚未检测"
      reading = .init(value: text, detail: "", reportDetail: "", tone: .pending)
    } else if let location = local?.location {
      reading = locationReading(location)
    } else if waiting {
      reading = .init(value: "正在检测…", detail: "", reportDetail: "", tone: .pending)
    } else {
      reading = .init(value: "旧记录未采集", detail: "", reportDetail: "", tone: .pending)
    }
    return [
      Row(
        title: locationTitle, symbol: "location", value: reading.value, detail: reading.detail,
        tone: reading.tone)
    ]
  }

  /// Report variant of the combined location row's 依据.
  public static func locationReportDetail(local: NetworkDiagnosticEnvironment?) -> String {
    guard let location = local?.location else { return "旧记录未采集或尚未检测" }
    return locationReading(location).reportDetail
  }

  private static func locationReading(_ location: LocationExposure) -> LocationReading {
    let system = systemReading(location)
    let browser = browserReading(location)
    let value: String
    switch location.systemEnabled {
    case nil:
      value = "位置暴露风险待确认 · 无法读取系统定位开关"
    case false?:
      value = "系统定位已关闭 · 应用与浏览器的定位授权不生效"
    case true?:
      if location.authorizationsReadable {
        let apps = location.apps.filter(\.allowed).count
        let browsers = location.browsers.filter { $0.authorization == .allowed }.count
        value =
          apps + browsers > 0
          ? "存在位置暴露风险 · 已授权 \(apps) 个应用、\(browsers) 个浏览器"
          : "存在潜在风险 · 系统定位已开启，未发现应用或浏览器授权"
      } else {
        value = "存在潜在风险 · 系统定位已开启，授权清单无法读取"
      }
    }
    let boundary = "授权表示具备获取位置的能力，不代表已经获取或上传。"
    return .init(
      value: value,
      detail: "系统定位\n" + system.detail + "\n\n浏览器定位\n" + browser.detail + "\n" + boundary,
      reportDetail: "系统定位：" + system.reportDetail + "\n浏览器定位：" + browser.reportDetail + "\n"
        + boundary,
      tone: system.tone)
  }

  private static func systemReading(_ location: LocationExposure) -> (
    detail: String, reportDetail: String, tone: Tone
  ) {
    let readable = location.authorizationsReadable
    let allowed = location.apps.filter(\.allowed)
    let denied = location.apps.filter { !$0.allowed }
    func line(_ app: LocationExposure.App) -> String {
      app.name + (app.path.isEmpty ? "（" + app.bundleID + "，无路径）" : " · " + app.path)
    }
    var lines: [String] = []
    var counts = ""
    if readable {
      if !allowed.isEmpty {
        lines.append("已授权定位的应用：\n" + allowed.map(line).joined(separator: "\n"))
      }
      if !denied.isEmpty {
        lines.append("已明确拒绝的应用：\n" + denied.map(line).joined(separator: "\n"))
      }
      if lines.isEmpty { lines.append("没有应用被授权或拒绝定位") }
      counts = "已授权 \(allowed.count) 个应用，已拒绝 \(denied.count) 个"
    } else {
      lines.append("无法读取系统定位授权清单（/var/db/locationd/clients.plist），应用授权无法读取")
      counts = "应用授权无法读取"
    }
    let tail = "\n" + lines.joined(separator: "\n") + "\n系统服务未列出"
    switch location.systemEnabled {
    case nil:
      return (
        "无法读取定位服务总开关，不视为已关闭。" + tail,
        "总开关无法读取；" + counts, .pending
      )
    case false?:
      return (
        "定位服务总开关：关闭；保留的应用授权不生效" + tail,
        "总开关关闭；" + counts, .information
      )
    case true?:
      return (
        "定位服务总开关：开启" + tail + "\n修改路径：" + systemPath,
        "总开关开启；" + counts, .difference
      )
    }
  }

  private static let browserBoundary =
    "不读取浏览器内的网站定位设置（macOS 应用数据保护）；系统授权的浏览器可由网站请求定位"

  private static func browserReading(_ location: LocationExposure) -> (
    detail: String, reportDetail: String
  ) {
    guard location.authorizationsReadable else {
      return (
        "无法读取系统定位授权清单（/var/db/locationd/clients.plist），不视为已关闭。\n" + notScanned,
        "授权清单无法读取"
      )
    }
    let states = location.browsers.map { ($0, LocationExposure.state($0)) }
    var lines = states.map { browser, state in browserLine(browser, state) }
    if lines.isEmpty { lines = ["未发现可打开网页的浏览器"] }
    let redacted = states.map { browser, state in browser.name + "：" + stateText(state) }
    let reportDetail = (redacted.isEmpty ? ["未发现浏览器"] : redacted).joined(separator: "\n")
    if location.systemEnabled == false {
      return (
        offNote + "\n" + lines.joined(separator: "\n") + "\n" + notScanned,
        offNote + "\n" + reportDetail
      )
    }
    let detail =
      lines.joined(separator: "\n") + "\n修改路径：" + systemPath + "\n" + browserBoundary + "\n"
      + notScanned
    return (detail, reportDetail)
  }

  private static func stateText(_ state: LocationExposure.State) -> String {
    switch state {
    case .open: "未关闭"
    case .closed: "已关闭"
    }
  }

  private static func browserLine(
    _ browser: LocationExposure.Browser, _ state: LocationExposure.State
  ) -> String {
    let reason: String
    switch browser.authorization {
    case .allowed: reason = "系统已授权定位"
    case .denied: reason = "系统已拒绝"
    case .none: reason = "系统未授权"
    }
    return browser.name + "：" + stateText(state) + "（" + reason + "）"
  }
}
