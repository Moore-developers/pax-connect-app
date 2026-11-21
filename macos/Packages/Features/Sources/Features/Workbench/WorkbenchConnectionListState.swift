import Foundation
import PaxCore

struct WorkbenchConnectionListState {
  var target = ""
  var exit = ""
  var rule = ""
  var sortColumn = 0
  var ascending = false

  func visible(_ rows: [WorkbenchTrafficRecord], context: PresentationContext = .chinese)
    -> [WorkbenchTrafficRecord]
  {
    rows.filter {
      (target.isEmpty
        || ($0.target(context: context) + " " + $0.connectionAddress)
          .localizedCaseInsensitiveContains(target))
        && (exit.isEmpty || $0.exitIdentity == exit)
        && (rule.isEmpty || $0.ruleIdentity == rule)
    }.sorted { a, b in
      let order: ComparisonResult
      switch sortColumn {
      case 0: order = a.started.compare(b.started)
      case 1:
        order = a.target(context: context).localizedStandardCompare(b.target(context: context))
      case 2:
        order = a.actualConnection(context: context).localizedStandardCompare(
          b.actualConnection(context: context))
      case 3:
        order = a.connectionRuleTitle(context: context).localizedStandardCompare(
          b.connectionRuleTitle(context: context))
      default:
        if (a.establishmentMS == nil) != (b.establishmentMS == nil) {
          return a.establishmentMS != nil
        }
        let av = a.establishmentMS ?? 0
        let bv = b.establishmentMS ?? 0
        order =
          av == bv
          ? a.connectionResult(context: context).localizedStandardCompare(
            b.connectionResult(context: context))
          : av < bv ? .orderedAscending : .orderedDescending
      }
      if order == .orderedSame { return a.id < b.id }
      return ascending ? order == .orderedAscending : order == .orderedDescending
    }
  }
}
