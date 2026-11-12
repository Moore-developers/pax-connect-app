import Foundation
import PaxProto
import Testing

@testable import PaxClient

@Suite("会话分析协议映射") struct AISessionMappingTests {
  @Test func summaryPreservesPricedDenominatorsUnavailableDaysAndComparison() {
    let summary = AISessionLimitSummary(
      .with {
        $0.bands = [
          .with {
            $0.requests = 4
            $0.pricedRequests = 3
            $0.money.official = 0.0000009
            $0.money.channel = 0.0000003
          }, .init(), .init(),
        ]
        $0.multiple = 0
        $0.inputTokens = 9_000_000_000
        $0.days = [
          .with {
            $0.atMs = 1_000
            $0.sessions = 2
            $0.requests = 4
            $0.pricedRequests = 3
            $0.inputTokens = 0
            $0.input.official = 0.0000009
            $0.available = true
          },
          .with { $0.atMs = 86_401_000 },
        ]
        $0.observedThroughMs = 10_000
        $0.comparison = .with {
          $0.sessions = 1
          $0.inputTokens = 4_000_000_000
          $0.input.official = 0.000001
          $0.fromMs = 100
          $0.toMs = 900
        }
      })
    #expect(summary.inputTokens == 9_000_000_000)
    #expect(summary.days[0].inputTokens == 0 && summary.days[1].inputTokens == nil)
    #expect(summary.comparison?.inputTokens == 4_000_000_000)
    #expect(summary.bands[0].requests == 4 && summary.bands[0].pricedRequests == 3)
    #expect(summary.bands[0].money.official == 0.0000009)
    #expect(summary.multiple == 0)
    #expect(summary.days[0].at == Date(timeIntervalSince1970: 1))
    #expect(summary.days[0].sessions == 2 && summary.days[0].pricedRequests == 3)
    #expect(summary.days[0].available && !summary.days[1].available)
    #expect(summary.observedThrough == Date(timeIntervalSince1970: 10))
    #expect(summary.comparison?.input.official == 0.000001)
    #expect(summary.comparison?.sessions == 1)
    #expect(summary.comparison?.from == Date(timeIntervalSince1970: 0.1))
    #expect(summary.comparison?.to == Date(timeIntervalSince1970: 0.9))
    let absent = AISessionLimitSummary(.init())
    #expect(absent.multiple == nil && absent.comparison == nil && absent.observedThrough == nil)
    #expect(absent.days.isEmpty && absent.inputTokens == nil)
  }

  @Test func limitsPreserveUnknownValuesAndZeroIndexMarkers() {
    let analysis = AISessionAnalysis(
      .with {
        $0.limits = .with {
          $0.summary.input = .with {
            $0.official = 0.1234567
            $0.channel = 0.01234567
          }
          $0.primary = [
            .with {
              $0.id = "s:codex:root"
              $0.cli = "codex"
              $0.sessionID = "root"
              $0.multiple = 0
              $0.lines = .with {
                $0.limit = 272000
                $0.warn = 250000
              }
            }
          ]
          $0.trend = .with {
            $0.overStart = 0
            $0.requests = [
              .with {
                $0.turnNumber = 0
                $0.request.totals.costsMicro = [1, 2, 3, 999]
                $0.request.pricedRequests = 1
              }
            ]
          }
        }
      })
    #expect(analysis.limits?.summary.input.official == 0.1234567)
    #expect(analysis.limits?.primary.first?.multiple == 0)
    #expect(analysis.limits?.primary.first?.overSpend == nil)
    #expect(analysis.limits?.primary.first?.lines?.baseline == nil)
    #expect(analysis.limits?.trend?.overStart == 0)
    #expect(analysis.limits?.trend?.flatStart == nil)
    #expect(analysis.limits?.trend?.requests.first?.turnNumber == 0)
    #expect(analysis.limits?.trend?.requests.first?.request.totals.costs[2] == 0.000003)
    #expect(AISessionAnalysis(.init()).limits == nil)
  }
  @Test func nativeDirectoryMapsEmptyUsageIdentityAndSourceStates() {
    let entry = Pax_V1_AISessionCatalogEntry.with {
      $0.key = .with {
        $0.cli = "dsh"
        $0.sessionID = "native"
      }
      $0.title = "原生标题"
      $0.titleSource = "dsh.title"
      $0.titleStatus = "named"
      $0.lastActivityMs = 1234
      $0.unlinkedChild = true
    }
    let page = AISessionCatalogPage(
      .with {
        $0.items = [entry]
        $0.selected = entry
        $0.revision = 8
        $0.usageRevision = 19
        $0.ready = true
        $0.total = 1
        $0.sources = [
          .with {
            $0.cli = "claude"
            $0.errorCode = "source_unavailable"
          }
        ]
      })
    #expect(page.items.first?.key == .init(cli: "dsh", session: "native"))
    #expect(page.selected?.title == "原生标题")
    #expect(page.items.first?.unlinkedChild == true && page.usageRevision == 19)
    #expect(page.items.first?.titleSource == "dsh.title")
    #expect(page.sourceErrors["claude"] == "source_unavailable")
    let state = AIUsageCollectionState(
      .with {
        $0.catalogRevision = 8
        $0.catalogReady = true
      })
    #expect(state.revision == 0 && state.catalogRevision == 8 && state.catalogReady)
  }
  @Test func retainsIdentityCompletionAndMicroDollarCosts() {
    var proto = Pax_V1_AIUsageAnalysis()
    proto.state.revision = 12
    proto.state.generation = 4
    proto.truncated = true
    proto.rows = [
      .with {
        $0.cli = "codex"
        $0.sessionID = "s"
        $0.sessionTitle = "优化会话分析"
        $0.turnID = "t"
        $0.modelID = "m"
        $0.reasoningEffort = "high"
        $0.atMs = 1000
        $0.endMs = 2000
        $0.complete = true
        $0.compressed = true
        $0.totals.count = 2
        $0.totals.costsMicro = [1, 2, 3, 4]
        $0.totals.costMicro = 5
        $0.totals.billedCostsMicro = [1, 1, 1, 2]
        $0.completedTurnIds = ["t"]
        $0.knownTurnRequests = 2
      }
    ]
    proto.requests = proto.rows
    proto.requests[0].requestID = "request-1"
    proto.contextUsage = [
      .with {
        $0.requests = 2
        $0.modelID = "m"
        $0.reasoningEffort = "xhigh"
        $0.turns = 1
        $0.knownTurnRequests = 1
        $0.subagents = 3
        $0.subagentsIncomplete = true
        $0.subagentBands = [
          .with {
            $0.input = 20
            $0.officialCostMicro = 150
            $0.channelCostMicro = 30
          }, .init(), .init(),
        ]
        $0.bands = [
          .with {
            $0.input = 150_000
            $0.officialCostMicro = 123_456
            $0.channelCostMicro = 12_345
            $0.requests = 2
            $0.pricedRequests = 1
          }, .init(), .init(),
        ]
      }
    ]
    let page = AISessionAnalysis(proto)
    #expect(page.requests.count == 1 && page.requests[0].requestID == "request-1")
    let row = page.rows[0]
    #expect(page.state.revision == 12 && page.state.generation == 4 && page.truncated)
    #expect(row.sessionTitle == "优化会话分析")
    #expect(row.key == AISessionKey(cli: "codex", session: "s"))
    #expect(row.turn == "t" && row.complete && row.compressed && row.effort == "high")
    #expect(
      abs(row.totals.cost - 0.000005) < 1e-12
        && abs(row.totals.costs.reduce(0, +) - 0.000010) < 1e-12)
    #expect(row.completedTurnIDs == ["t"] && row.knownRequests == 2)
    let context = page.contextUsage[0]
    #expect(
      context.session == nil && context.model == "m" && context.effort == "xhigh"
        && context.bands.count == 3)
    #expect(
      context.subagents == 3 && context.subagentsIncomplete && context.subagentBands[0].input == 20)
    #expect(
      context.subagentBands[0].officialCost == 0.000150
        && context.subagentBands[0].channelCost == 0.000030)
    #expect(context.turns == 1 && context.requests == 2 && context.knownTurnRequests == 1)
    #expect(context.input == 150_000 && context.bands[0].pricedRequests == 1)
    #expect(context.bands[0].officialCost == 0.123456 && context.bands[0].channelCost == 0.012345)
  }
}
