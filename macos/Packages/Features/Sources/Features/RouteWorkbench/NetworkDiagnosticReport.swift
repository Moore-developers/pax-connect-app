import AppKit
import PaxCore
import PaxUI

extension NetworkDiagnosticView {
  func reportText() -> String {
    var output = P.markdown(
      report, services: store.services, environment: store.environment, stale: store.stale)
    if let previous = store.previousReport, previous.at != report?.at,
      report?.providerError.isEmpty == false
    {
      output +=
        "\n\n## 上次线路证据（历史，不代表本轮）\n"
        + P.markdown(previous, services: nil, environment: nil, stale: true)
    }
    if let environment = store.currentEnvironment { output += "\n\n" + E.markdown(environment) }
    var identities = [
      report?.exit?.ip ?? "", report?.targetRouteID ?? "", report?.targetRouteName ?? "",
    ]
    identities +=
      report?.facts.filter { $0.group == "network" && $0.label == "宣告前缀" }.map(\.value) ?? []
    identities += connection?.routesStore?.routes.flatMap { [$0.id, $0.name] } ?? []
    for identity in identities.filter({ !$0.isEmpty }).sorted(by: { $0.count > $1.count }) {
      output = output.replacingOccurrences(of: identity, with: "[已脱敏]")
    }
    return output
  }
}
