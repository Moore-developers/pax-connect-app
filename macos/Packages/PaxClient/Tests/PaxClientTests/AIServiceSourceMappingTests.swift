import Foundation
import PaxProto
import Testing

@testable import PaxClient

@Suite("官方账号与 API 来源映射")
struct AIServiceSourceMappingTests {
  @Test func accountNeedsNoProfile() {
    let wire = Pax_V1_AIProfilesWorkspace.with {
      $0.bindings = [
        .with {
          $0.cli = "codex"
          $0.state = "account"
          $0.serviceSource = "official_account"
        }
      ]
    }
    let workspace = AIProfilesWorkspace(wire)
    #expect(workspace.profiles.isEmpty)
    #expect(workspace.bindings[.codex]?.profileID == "")
    #expect(workspace.bindings[.codex]?.serviceSource == .officialAccount)
  }

  @Test func equivalentsRemainDistinctFromChannelEstimates() throws {
    var wire = Pax_V1_AIUsageRequest.with {
      $0.cli = "claude"
      $0.serviceSource = "official_account"
      $0.costsMicro = [3_000_000, 0, 0, 0]
      $0.costMicro = 3_000_000
      $0.multiplier = 1
    }
    let account = try #require(AIUsageRequest(wire))
    #expect(account.originalCost == 3)
    #expect(!account.hasChannelEstimate)
    #expect(account.sourceLabel == "官方账号")
    wire.serviceSource = "relay_api"
    wire.profileName = "Example"
    let relay = try #require(AIUsageRequest(wire))
    #expect(relay.hasChannelEstimate && relay.sourceLabel == "Example")
    wire.serviceSource = "future_unrecognized_auth"
    #expect(AIUsageRequest(wire)?.serviceSource == .unknown)
    #expect(AIUsageRequest(wire)?.hasChannelEstimate == false)
  }

  @Test func mixedTotalsDoNotLoseUnknownChannelCoverage() {
    let account = AIUsageTotals(
      .with {
        $0.count = 2
        $0.unconfirmedChannel = 2
        $0.costsMicro = [6_000_000, 0, 0, 0]
      })
    var mixed = AIUsageTotals(.with { $0.count = 1 })
    mixed.add(account)
    #expect(mixed.count == 3 && mixed.unconfirmedChannel == 2)
    let money = AISessionLimitMoney(
      .with {
        $0.official = 6
        $0.channel = 6
        $0.channelUnknown = true
      })
    #expect(money.channelUnknown && money.official == 6)
  }

  @Test func officialAPIRequiresAnExactSecureEndpoint() {
    #expect(AIServiceSource.api(endpoint: "https://api.openai.com/v1") == .officialAPI)
    #expect(AIServiceSource.api(endpoint: "https://api.anthropic.com") == .officialAPI)
    for endpoint in [
      "https://api.openai.com.example/v1", "http://api.openai.com/v1",
      "https://api.openai.com:8443/v1", "https://api.openai.com/proxy",
    ] {
      #expect(AIServiceSource.api(endpoint: endpoint) == .relayAPI)
    }
  }
}
