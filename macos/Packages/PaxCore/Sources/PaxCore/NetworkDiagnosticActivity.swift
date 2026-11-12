import Foundation

public enum NetworkDiagnosticRunState: Sendable { case idle, running, finished, stopped, failed }

/// Live foreground activity is separate from retained diagnostic evidence.
public struct NetworkDiagnosticActivity {
  public enum State { case idle, running, finished, partial, stopped, failed }
  public let name, detail: String
  public let state: State
  public var settled: Bool { state != .idle && state != .running }
}

extension NetworkDiagnosticStore {
  public var activities: [NetworkDiagnosticActivity] {
    var values = [routeActivity]
    if let currentEnvironment {
      values.append(currentEnvironment.activity(browser: false))
      values.append(currentEnvironment.activity(browser: true))
    }
    return values
  }

  private var routeActivity: NetworkDiagnosticActivity {
    let name = "线路检测"
    switch runState {
    case .idle: return .init(name: name, detail: "尚未开始", state: .idle)
    case .running:
      var parts = [String]()
      if let progress {
        if progress.probesTotal > 0 {
          parts.append("已结束探测 \(progress.probesCompleted)/\(progress.probesTotal)")
        }
        parts.append(Self.phase(progress.exitState, title: "出口"))
        parts.append(Self.phase(progress.sourcesState, title: "IP 资料"))
      } else {
        parts.append("正在准备线路与探测任务")
      }
      if let progressError { parts.append(progressError) }
      return .init(name: name, detail: parts.joined(separator: " · "), state: .running)
    case .finished:
      let partial =
        report?.probes.contains { !$0.errorCode.isEmpty } == true
        || report?.providers?.contains {
          !["observed", "not_indexed", "unsupported"].contains($0.state)
        } == true
        || !(report?.providerError.isEmpty ?? true)
      return .init(
        name: name, detail: "已结束 \(report?.probes.count ?? 0) 项探测" + (partial ? " · 部分项目未完成" : ""),
        state: partial ? .partial : .finished)
    case .stopped: return .init(name: name, detail: "已停止，保留已有结果", state: .stopped)
    case .failed: return .init(name: name, detail: message, state: .failed)
    }
  }

  private static func phase(_ state: String, title: String) -> String {
    switch state {
    case "running": return title + "查询中"
    case "complete": return title + "已返回"
    case "failed": return title + "查询失败"
    case "skipped": return title + "未执行"
    default: return "等待" + title + "查询"
    }
  }
}

extension EnvironmentDiagnosticStore {
  public func activity(browser: Bool) -> NetworkDiagnosticActivity {
    let name = browser ? "浏览器检测" : "Mac 检测"
    let sample = browser ? progressReport?.browser : progressReport?.mac
    if let sample, sample.complete {
      let partial =
        !sample.dnsError.isEmpty || !sample.ipError.isEmpty
        || sample.checks?.contains { ["failed", "timeout", "unsupported"].contains($0.state) }
          == true
      return .init(
        name: name, detail: partial ? "已结束，部分项目未完成" : "已结束，结果已返回",
        state: partial ? .partial : .finished)
    }
    switch runState {
    case .idle: return .init(name: name, detail: "尚未开始", state: .idle)
    case .running:
      let sampled = !(sample?.checks?.isEmpty ?? true)
      return .init(
        name: name,
        detail: sampled ? "采样已返回，正在查询 IP 资料" : browser ? "等待浏览器采样回传" : "正在采样公网 IP 与 DNS",
        state: .running)
    case .stopped: return .init(name: name, detail: "已停止，保留已有结果", state: .stopped)
    case .finished, .failed: return .init(name: name, detail: "未完成 · " + message, state: .failed)
    }
  }
}
