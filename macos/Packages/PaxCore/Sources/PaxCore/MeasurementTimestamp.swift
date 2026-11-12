import Foundation

/// Shared wall-clock presentation. Values remain timestamps across IPC/storage.
@MainActor public enum MeasurementTimestamp {
  private static let formatter: DateFormatter = {
    let value = DateFormatter()
    value.calendar = Calendar(identifier: .gregorian)
    value.locale = Locale(identifier: "en_US_POSIX")
    value.dateFormat = "yyyy-MM-dd HH:mm"
    return value
  }()
  private static func format(_ date: Date, _ pattern: String) -> String {
    formatter.timeZone = .current
    formatter.dateFormat = pattern
    defer { formatter.dateFormat = "yyyy-MM-dd HH:mm" }
    return formatter.string(from: date)
  }
  public static func text(_ date: Date?) -> String {
    guard let date else { return "—" }
    return format(date, "yyyy-MM-dd HH:mm")
  }
  /// 带秒，用于同一分钟内要区分两次操作的位置。
  public static func secondsText(_ date: Date) -> String { format(date, "yyyy-MM-dd HH:mm:ss") }
  /// 不带年份，用于列表里的次要行。
  public static func shortText(_ date: Date) -> String { format(date, "MM-dd HH:mm") }
  public static func chartTime(_ date: Date, includesDate: Bool = false) -> String {
    date.formatted(
      includesDate
        ? .dateTime.month(.twoDigits).day(.twoDigits).hour(.twoDigits(amPM: .omitted)).minute(
          .twoDigits) : .dateTime.hour(.twoDigits(amPM: .omitted)).minute(.twoDigits))
  }

}
