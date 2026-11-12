import Foundation

/// The only hover / pin / keyboard state machine for the shared time-series chart.
/// Selection is identified by bucket date, never by array index, so it survives refreshes.
struct PaxTimeSeriesInteraction: Equatable {
  private(set) var selected: Date?
  private(set) var sampleID: String?
  private(set) var pinned = false

  mutating func hover(_ hit: PaxTimeSeriesLayout.Hit?) {
    guard !pinned else { return }
    selected = hit?.date
    sampleID = hit?.sampleID
  }

  /// Click pins the bucket under the pointer; clicking again releases it.
  mutating func click(_ hit: PaxTimeSeriesLayout.Hit?) {
    if pinned {
      escape()
    } else {
      selected = hit?.date
      sampleID = hit?.sampleID
      pinned = selected != nil
    }
  }

  /// Arrow keys move the selection and keep the current pin state.
  mutating func move(_ hit: PaxTimeSeriesLayout.Hit?) {
    guard let hit else { return }
    selected = hit.date
    sampleID = hit.sampleID
  }

  mutating func leave() {
    if !pinned { selected = nil }
  }

  mutating func escape() {
    pinned = false
    selected = nil
    sampleID = nil
  }

  mutating func pin(at date: Date, sampleID: String? = nil) {
    self.sampleID = sampleID
    selected = date
    pinned = true
  }

  /// After a data refresh a pin stays while its bucket still exists; hover is dropped.
  mutating func refresh(in layout: PaxTimeSeriesLayout) {
    guard let selected, pinned, layout.resolve(selected, sampleID: sampleID) != nil else {
      escape()
      return
    }
  }
}

/// Remembers a pinned bucket and keyboard focus across a chart rebuild (the host recreates
/// the view on refresh). An entry is restorable only briefly after its chart left the window.
@MainActor
enum PaxTimeSeriesPinMemory {
  struct Restored: Equatable {
    var date: Date?
    var sampleID: String? = nil
    var focused: Bool
  }
  private struct Entry {
    var date: Date?
    var sampleID: String? = nil
    var focused = false
    var releasedAt: Date?
  }
  private static var entries: [String: Entry] = [:]

  static func remember(_ date: Date, sampleID: String? = nil, key: String) {
    entries[key] = Entry(date: date, sampleID: sampleID, focused: false, releasedAt: nil)
  }

  static func forget(key: String) { entries.removeValue(forKey: key) }

  static func release(
    _ date: Date?, sampleID: String? = nil, focused: Bool, key: String, now: Date = Date()
  ) {
    guard date != nil || focused else { return }
    entries[key] = Entry(date: date, sampleID: sampleID, focused: focused, releasedAt: now)
  }

  static func take(key: String, now: Date = Date()) -> Restored? {
    guard let entry = entries.removeValue(forKey: key), let released = entry.releasedAt,
      now.timeIntervalSince(released) <= PaxTheme.TimeSeries.pinRestoreWindow
    else { return nil }
    return Restored(date: entry.date, sampleID: entry.sampleID, focused: entry.focused)
  }
}
