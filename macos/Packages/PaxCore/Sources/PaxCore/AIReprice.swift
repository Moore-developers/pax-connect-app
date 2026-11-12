import Foundation
import Observation
import PaxClient

/// Editor input for a multiplier: at most 3 decimals, 0...100; empty means "inherit".
public enum AIMultiplierInput: Equatable, Sendable {
  case empty
  case value(Double)
  case invalid

  public static let errorText = "倍率最多 3 位小数，范围 0–100"

  public static func parse(_ text: String) -> AIMultiplierInput {
    let trimmed = text.trimmingCharacters(in: .whitespaces)
    if trimmed.isEmpty { return .empty }
    let parts = trimmed.split(separator: ".", omittingEmptySubsequences: false)
    guard parts.count <= 2, parts.allSatisfy({ $0.allSatisfy { "0123456789".contains($0) } }),
      !parts[0].isEmpty, parts.count == 1 || (1...3).contains(parts[1].count),
      let value = Double(trimmed), (0...100).contains(value)
    else { return .invalid }
    return .value(value)
  }
}

/// The time choices of a repricing; converted to local calendar days for the daemon.
public enum AIRepriceWindow: Equatable, Sendable {
  case today
  case week
  case custom(Date, Date)
  case all
}

public enum AIReprice {
  /// "YYYY-MM-DD" in the calendar's time zone.
  public static func day(_ date: Date, calendar: Calendar = .current) -> String {
    let c = calendar.dateComponents([.year, .month, .day], from: date)
    return String(format: "%04d-%02d-%02d", c.year ?? 0, c.month ?? 0, c.day ?? 0)
  }
  /// 本周 runs from Monday of the local calendar week through today.
  public static func range(_ window: AIRepriceWindow, now: Date, calendar: Calendar = .current)
    -> AIRepriceRange
  {
    switch window {
    case .today: return .days(from: day(now, calendar: calendar), to: day(now, calendar: calendar))
    case .week:
      let sinceMonday = (calendar.component(.weekday, from: now) + 5) % 7
      let monday = calendar.date(byAdding: .day, value: -sinceMonday, to: now) ?? now
      return .days(from: day(monday, calendar: calendar), to: day(now, calendar: calendar))
    case .custom(let from, let to):
      return .days(from: day(from, calendar: calendar), to: day(to, calendar: calendar))
    case .all: return .all
    }
  }
  /// Dry-run summary, e.g. "将更新 12 条请求，费用 $1.000 → $0.800（其中 3 条按小时汇总估算）".
  public static func previewText(_ r: AIRepriceResult) -> String {
    guard r.requests > 0 else { return "所选范围没有可更新的请求" }
    var text =
      "将更新 \(r.requests) 条请求，费用 "
      + AIUsageFormatting.cost(r.oldCost) + " → " + AIUsageFormatting.cost(r.newCost)
    if r.aggregatedRequests > 0 { text += "（其中 \(r.aggregatedRequests) 条按小时汇总估算）" }
    return text
  }
  public static func doneText(_ r: AIRepriceResult) -> String {
    "已更新 \(r.requests) 条请求，费用 "
      + AIUsageFormatting.cost(r.oldCost) + " → " + AIUsageFormatting.cost(r.newCost)
  }
  public static func message(_ error: any Error) -> String {
    switch (error as? ProductError)?.code {
    case "ai_usage_busy": return "正在采集，请稍后重试"
    case "ai_profile_not_found": return "Profile 不存在，请刷新后重试"
    case "ai_usage_range_invalid": return "日期范围无效，请重新选择"
    case "ai_usage_unavailable": return "本地用量暂不可用，请稍后重试"
    default: return "更新失败，请稍后重试"
    }
  }
}

/// Preview-then-confirm flow of one repricing request; a real run only follows a preview of
/// the same request, and cancelling changes nothing.
@MainActor @Observable
public final class AIRepriceFlow {
  public enum State: Equatable {
    case idle
    case working
    case preview(AIRepriceResult)
    case done(AIRepriceResult)
    case failed(String)
  }
  public struct Request: Equatable, Sendable {
    public let profileID: String
    public let model: String?
    public let range: AIRepriceRange
    public init(profileID: String, model: String?, range: AIRepriceRange) {
      self.profileID = profileID
      self.model = model
      self.range = range
    }
  }
  public private(set) var state: State = .idle
  public var onChange: (() -> Void)?
  private let data: AIUsageDataStore?
  private var pending: Request?
  public init(data: AIUsageDataStore?) { self.data = data }
  public var available: Bool { data?.canReprice ?? false }

  public func preview(_ request: Request) async {
    guard state != .working else { return }
    await run(request, dryRun: true)
  }
  /// Runs the previewed request for real; ignored without a preview.
  public func confirm() async {
    guard case .preview = state, let request = pending else { return }
    await run(request, dryRun: false)
  }
  public func reset() {
    guard state != .working else { return }
    pending = nil
    set(.idle)
  }
  private func run(_ request: Request, dryRun: Bool) async {
    guard let data else { return set(.failed(AIReprice.message(ProductError(code: "")))) }
    set(.working)
    do {
      let result = try await data.reprice(
        profileID: request.profileID, model: request.model, range: request.range, dryRun: dryRun)
      if dryRun {
        pending = request
        set(.preview(result))
      } else {
        pending = nil
        set(.done(result))
      }
    } catch {
      pending = nil
      set(.failed(AIReprice.message(error)))
    }
  }
  private func set(_ value: State) {
    state = value
    onChange?()
  }
}
