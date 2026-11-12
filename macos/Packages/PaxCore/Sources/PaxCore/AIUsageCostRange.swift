import Foundation

public enum AIUsageCostRangeError: Error {
  case amount, order
  public var message: String {
    switch self {
    case .amount: "请输入非负美元金额，最多6位小数；金额过大时请缩小范围"
    case .order: "最低费用不能高于最高费用"
    }
  }
}

extension AIUsageFormatting {
  /// Parses exact, inclusive USD bounds into the integer micro-USD query contract.
  public static func costBounds(minimum: String, maximum: String)
    throws(AIUsageCostRangeError) -> (minimum: Int64?, maximum: Int64?)
  {
    let lower = try costBound(minimum)
    let upper = try costBound(maximum)
    if let lower, let upper, lower > upper { throw .order }
    return (lower, upper)
  }

  private static func costBound(_ input: String) throws(AIUsageCostRangeError) -> Int64? {
    let raw = input.trimmingCharacters(in: .whitespacesAndNewlines)
    if raw.isEmpty { return nil }
    guard raw.utf8.allSatisfy({ (48...57).contains($0) || $0 == 46 }),
      raw.utf8.contains(where: { (48...57).contains($0) })
    else { throw .amount }
    let parts = raw.split(separator: ".", omittingEmptySubsequences: false)
    guard parts.count <= 2 else { throw .amount }
    let fraction = parts.count == 2 ? String(parts[1]) : ""
    guard fraction.count <= 6 else { throw .amount }
    let digits = String(parts[0]) + fraction + String(repeating: "0", count: 6 - fraction.count)
    guard let micros = Int64(digits) else { throw .amount }
    return micros
  }

  public static func costBoundInput(_ micro: Int64?) -> String {
    guard let micro else { return "" }
    let whole = micro / 1_000_000
    let fraction = micro % 1_000_000
    guard fraction != 0 else { return String(whole) }
    var digits = String(format: "%06lld", fraction)
    while digits.last == "0" { digits.removeLast() }
    return String(whole) + "." + digits
  }
}
