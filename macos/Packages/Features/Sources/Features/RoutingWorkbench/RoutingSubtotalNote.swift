import PaxClient
import PaxCore

extension RoutingWorkbenchStore {
  /// 应用页小计只含macOS应用与产品CLI；系统进程和未识别来源另列，使三者相加等于域名页合计。
  /// 两者都为 0 时返回 nil。
  static func subtotalNote(_ page: TrafficPage) -> String? {
    let system = page.systemUpload + page.systemDownload
    let unknown = page.unknownSource.map { $0.upload + $0.download } ?? 0
    guard system > 0 || unknown > 0 else { return nil }
    return
      "另有系统与进程 \(TrafficFormatting.bytes(system))、未识别来源 \(TrafficFormatting.bytes(unknown)) 未计入应用小计"
  }
}
