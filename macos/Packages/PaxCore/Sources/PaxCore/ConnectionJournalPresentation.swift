import Foundation

@MainActor public enum ConnectionJournalPresentation {
  public static func current(
    _ logs: [ProductLog], session: String, generation: UInt64, routeID: String, errorsOnly: Bool
  ) -> [ProductLog] {
    logs.filter { entry in
      guard let fact = entry.connection else { return false }
      return fact.session == session && fact.generation == generation && fact.routeID == routeID
        && (!errorsOnly || !fact.code.isEmpty)
    }.reversed()
  }
  public static func title(_ event: ConnectionEvent) -> String {
    let stage =
      [
        "opening": "接收连接", "resolution": "目标地址检查", "dial": "出站拨号返回", "received": "收到远端数据",
        "closed": "连接关闭", "udp_opened": "UDP 会话已创建", "udp_open": "UDP 会话创建失败",
        "udp_write": "UDP 发送失败", "udp_read": "UDP 接收失败",
        "runtime_tcp_read": "接收失败", "runtime_tcp_write": "发送失败",
      ][event.stage] ?? event.stage
    return stage + (event.code.isEmpty ? "" : " · " + event.code)
  }
  public static func detail(_ entry: ProductLog) -> String {
    guard let e = entry.connection else { return entry.message }
    return [
      MeasurementTimestamp.text(entry.at), title(e), "目标：" + e.target, "连接：" + e.connectionID,
      "入口：" + (e.inbound.isEmpty ? "未知" : e.inbound), "线路：" + e.routeID,
      "代次：\(e.generation) · 配置：\(e.configRevision)",
      "耗时：" + (MeasurementFormatting.latency(Double(e.durationMS))?.text ?? "—"),
      "拨号返回与收到数据均不等于目标网页已成功打开。",
    ].joined(separator: "\n")
  }
}
