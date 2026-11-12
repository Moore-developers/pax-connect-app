import Foundation
import PaxProto
import Testing

@testable import PaxClient

/// 映射测试：纯函数，不连 daemon、不碰网络（code-standards.md §三 的测试要求）。
/// 它保证「proto 字段改名/换编号后映射不会静默错位」。
@Suite("proto → 领域模型映射")
struct MappingTests {
  @Test("动态类别与通用块映射保留事件时快照")
  func dynamicEventContract() {
    let value = Pax_V1_LogRecord.with {
      $0.categoryName = "证书与信任"
      $0.categorySymbol = "checkmark.shield"
      $0.systemEvent = .with {
        $0.type = "trust.policy_changed"
        $0.name = "更新信任策略"
        $0.result = "completed"
        $0.category = .with {
          $0.key = "trust"
          $0.name = "证书与信任"
          $0.symbol = "checkmark.shield"
        }
        $0.sections = [
          .with {
            $0.kind = "changes"
            $0.title = "当时变化"
            $0.fields = [
              .with {
                $0.label = "策略"
                $0.before = "严格"
                $0.after = "系统信任"
              }
            ]
          }
        ]
      }
    }
    let row = LogRecord(value)
    #expect(row.systemEvent?.type == "trust.policy_changed" && row.categoryName == "证书与信任")
    #expect(row.systemEvent?.sections.first?.fields.first?.before == "严格")
    #expect(row.systemEvent?.category.symbol == "checkmark.shield")
    let object = HistoryObject(Pax_V1_LogObject.with { $0.symbol = "terminal" })
    #expect(object.symbol == "terminal")
  }
  @Test("系统目录scope和分类映射")
  func systemDirectoryContract() {
    var q = LogFilter()
    q.scope = "system"
    q.dimension = "system"
    q.objectID = "routes"
    q.level = "error"
    let raw = DaemonConnection.logRequest(q)
    #expect(
      raw.scope == "system" && raw.dimension == "system" && raw.objectID == "routes"
        && raw.level == "error")
    let row = LogRecord(Pax_V1_LogRecord.with { $0.systemCategory = "routes" })
    #expect(row.systemCategory == "routes")
  }
  @Test("访问筛选条件、阶段和操作上下文跨端不丢失")
  func logAccessContract() {
    var filter = LogFilter()
    filter.quick = "slow"
    filter.criteria.kinds = ["access", "dns"]
    filter.criteria.levels = ["warning", "error"]
    filter.criteria.domain = "api.x.com"
    filter.criteria.domainMode = "exact"
    filter.criteria.targetIP = "2001:db8::/32"
    filter.criteria.dnsAnswerIP = "192.0.2.0/24"
    filter.criteria.application = "ego"
    filter.criteria.minLatencyMS = 2000
    let request = DaemonConnection.logRequest(filter)
    #expect(request.quick == "slow" && request.criteria.minLatencyMs == 2000)
    #expect(
      request.criteria.kinds == ["access", "dns"]
        && request.criteria.levels == ["warning", "error"])
    #expect(request.criteria.domainMode == "exact" && request.criteria.targetIp == "2001:db8::/32")
    #expect(request.criteria.dnsAnswerIp == "192.0.2.0/24" && request.criteria.application == "ego")
    let row = LogRecord(
      Pax_V1_LogRecord.with {
        $0.operationFrom = "旧线路"
        $0.operationTo = "新线路"
        $0.flow = .with { $0.phase = "connecting" }
      })
    #expect(
      row.operationFrom == "旧线路" && row.operationTo == "新线路" && row.flow?.phase == "connecting")
  }

  @Test("DNS与缺失原因跨协议映射不丢字段")
  func dnsObservationMapping() {
    let raw = Pax_V1_FlowRecord.with {
      $0.purpose = "dns"
      $0.processStatus = "not_found"
      $0.dns = .with {
        $0.queryType = "AAAA"
        $0.result = "NOERROR"
        $0.answers = ["2001:db8::1"]
      }
    }
    let flow = FlowFact(raw)
    #expect(flow.purpose == "dns")
    #expect(flow.processStatus == "not_found")
    #expect(flow.dns?.answers == ["2001:db8::1"])
    #expect(FlowFact(Pax_V1_FlowRecord()).dns == nil)
    var filter = LogFilter()
    filter.includeDNS = true
    #expect(DaemonConnection.logRequest(filter).includeDns)
  }
  @Test("保护状态与日志级别的枚举映射不丢语义")
  func enumMapping() {
    #expect(ProtectionStatus(proto: .protected) == .protected)
    #expect(ProtectionStatus(proto: .degraded) == .degraded)
    #expect(ProtectionStatus(proto: .unspecified) == .off)
    #expect(LogLevel(proto: .warning) == .warning)
    #expect(LogLevel(proto: .UNRECOGNIZED(99)) == .info)
  }

  @Test("快照映射保留线路、当前线路、今日流量与最近事件")
  func snapshotMapping() {
    let proto = Pax_V1_Snapshot.with {
      $0.snapshotVersion = 7
      $0.status = .degraded
      $0.routes = [
        .with {
          $0.id = "r1"
          $0.name = "测试线路"
          $0.protocolName = "vless"
          $0.server = "example.com"
          $0.port = 443
          $0.group = "默认"
        }
      ]
      $0.currentRoute = .with {
        $0.id = "r1"
        $0.name = "测试线路"
      }
      $0.todayTraffic = .with {
        $0.uploadBytes = 10
        $0.downloadBytes = 90
        $0.proxiedBytes = 100
      }
      $0.settings = .with { $0.autoConnect = true }
    }

    let snapshot = DaemonSnapshot(proto: proto)
    #expect(snapshot.snapshotVersion == 7)
    #expect(snapshot.status == .degraded)
    #expect(snapshot.routes.count == 1)
    #expect(snapshot.routes.first?.protocolName == "vless")
    #expect(snapshot.currentRoute?.id == "r1")
    #expect(snapshot.todayTraffic.totalBytes == 100)
    #expect(snapshot.settings.autoConnect)
    #expect(snapshot.recentLogs.isEmpty)
  }

  @Test("事件映射：空 payload 返回 nil，AI 用量事件带版本号与代数")
  func eventMapping() {
    #expect(DaemonEvent(proto: Pax_V1_WatchEvent()) == nil)

    let usage = Pax_V1_WatchEvent.with {
      $0.aiUsage = .with {
        $0.revision = 9
        $0.generation = 2
        $0.backfilling = true
      }
    }
    guard case .aiUsage(let state) = DaemonEvent(proto: usage) else {
      Issue.record("期望 .aiUsage 事件")
      return
    }
    #expect(state.revision == 9)
    #expect(state.generation == 2)
    #expect(state.backfilling)
  }

  @Test("首帧快照带 AI 用量状态；线路历史版本号映射到 HistoryState")
  func pushContractFields() {
    let proto = Pax_V1_Snapshot.with {
      $0.aiUsage = .with { $0.revision = 4 }
      $0.history = .with { $0.routeHistoryRevision = 6 }
    }
    let snapshot = DaemonSnapshot(proto: proto)
    #expect(snapshot.aiUsage?.revision == 4)
    #expect(snapshot.history.routeHistoryRevision == 6)
    #expect(DaemonSnapshot(proto: Pax_V1_Snapshot()).aiUsage == nil)
  }

  @Test("外部TUN与出口错误参数映射；空值为nil或空数组")
  func externalTUNAndExitErrorParams() {
    let snapshot = DaemonSnapshot(
      proto: Pax_V1_Snapshot.with {
        $0.externalTun = .with {
          $0.interface = "utun6"
          $0.target = "128.0/1"
          $0.at = .init(date: Date(timeIntervalSince1970: 1_700_000_000))
        }
      })
    #expect(snapshot.externalTUN?.interface == "utun6")
    #expect(snapshot.externalTUN?.target == "128.0/1")
    #expect(snapshot.externalTUN?.at == Date(timeIntervalSince1970: 1_700_000_000))
    #expect(DaemonSnapshot(proto: Pax_V1_Snapshot()).externalTUN == nil)

    let exit = RouteExitSummary(
      proto: Pax_V1_RouteExitSummary.with {
        $0.errorCode = "exit_ws_handshake_failed"
        $0.errorParams = ["403"]
      })
    #expect(exit.errorParams == ["403"])
    #expect(RouteExitSummary(proto: Pax_V1_RouteExitSummary()).errorParams.isEmpty)
  }
}

@Test("未知TLS模式不能映射成跳过或已验证")
func unknownTLSMode() {
  #expect(TLSMode(proto: .UNRECOGNIZED(99)) == .unknown)
  let proto = Pax_V1_ActiveTLS()
  let active = ActiveTLS(proto: proto)
  #expect(active.mode == .unknown)
  #expect(!active.verified)
  #expect(!active.skipped)
}

extension MappingTests {
  @Test("查询池协议保留父批次/临时阶段/真实计数，保存配置不回写运行态")
  func exitPoolMapping() {
    let pool = Pax_V1_ExitPoolState.with {
      $0.configuredLimit = 8
      $0.inFlight = 4
      $0.queued = 12
      $0.retrying = 2
      $0.availableProviders = 5
      $0.totalProviders = 7
    }
    let snapshot = DaemonSnapshot(
      proto: .with {
        $0.latencyOperationID = "parent"
        $0.testOperationIds = ["waiting-route": "queued-operation"]
        $0.exitPoolState = pool
        $0.routeMeasurementProgress = [
          .with {
            $0.routeID = "route"
            $0.operationID = "child"
            $0.testedConfigRevision = 7
            $0.phase = .exitQueued
            $0.latencyResult = .with {
              $0.success = true
              $0.stableMs = 20
              $0.stableSamples = 3
            }
          }
        ]
      })
    #expect(snapshot.latencyOperationID == "parent")
    #expect(snapshot.testOperationIDs["waiting-route"] == "queued-operation")
    #expect(snapshot.exitPoolState?.queued == 12)
    #expect(snapshot.routeMeasurementProgress.first?.phase == .exitQueued)
    #expect(snapshot.routeMeasurementProgress.first?.latencyResult?.stableMS == 20)
    let preferences = MeasurementPreferences(proto: .with { $0.exitPoolState = pool })
    #expect(preferences.exitPoolState?.inFlight == 4)
    #expect(!preferences.proto.hasExitPoolState)
    #expect(DaemonSnapshot(proto: .init()).exitPoolState == nil)
  }
}

extension MappingTests {
  @Test("下载队列快照保留模式、排队顺序与按线路取消身份")
  func downloadQueueMapping() {
    let snapshot = DaemonSnapshot(
      proto: .with {
        $0.downloadQueue = .with {
          $0.mode = "single"
          $0.activeRouteID = "a"
          $0.queuedRouteIds = ["b", "c"]
          $0.operationIds = ["a": "active", "b": "queued-b"]
        }
      })
    #expect(snapshot.downloadQueue?.mode == "single")
    #expect(snapshot.downloadQueue?.queuedRouteIDs == ["b", "c"])
    #expect(snapshot.downloadQueue?.operationIDs["b"] == "queued-b")
    #expect(DaemonSnapshot(proto: .init()).downloadQueue == nil)
  }
}
