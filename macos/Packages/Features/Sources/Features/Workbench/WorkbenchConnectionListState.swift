import Foundation

struct WorkbenchConnectionListState {
  var target = ""
  var exit = ""
  var rule = ""
  var sortColumn = 0
  var ascending = false

  func visible(_ rows: [WorkbenchTrafficRecord]) -> [WorkbenchTrafficRecord] {
    rows.filter {
      (target.isEmpty
        || ($0.target + " " + $0.connectionAddress).localizedCaseInsensitiveContains(target))
        && (exit.isEmpty || $0.actualConnection == exit)
        && (rule.isEmpty || $0.connectionRuleTitle == rule)
    }.sorted { a, b in
      let order: ComparisonResult
      switch sortColumn {
      case 0: order = a.started.compare(b.started)
      case 1: order = a.target.localizedStandardCompare(b.target)
      case 2: order = a.actualConnection.localizedStandardCompare(b.actualConnection)
      case 3: order = a.connectionRuleTitle.localizedStandardCompare(b.connectionRuleTitle)
      default:
        if (a.establishmentMS == nil) != (b.establishmentMS == nil) {
          return a.establishmentMS != nil
        }
        let av = a.establishmentMS ?? 0
        let bv = b.establishmentMS ?? 0
        order =
          av == bv
          ? a.connectionResult.localizedStandardCompare(b.connectionResult)
          : av < bv ? .orderedAscending : .orderedDescending
      }
      if order == .orderedSame { return a.id < b.id }
      return ascending ? order == .orderedAscending : order == .orderedDescending
    }
  }
}
