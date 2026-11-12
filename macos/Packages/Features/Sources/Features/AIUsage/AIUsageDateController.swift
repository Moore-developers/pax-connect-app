import AppKit
import PaxUI

@MainActor
final class AIUsageDateController: NSViewController {
  private let store: AIUsageStore
  private var picker: PaxDateRangePicker?
  var onApply: (() -> Void)?
  init(store: AIUsageStore) {
    self.store = store
    super.init(nibName: nil, bundle: nil)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    let picker = PaxDateRangePicker(
      start: store.start, end: store.end, calendar: store.calendar, now: store.now,
      options: .init(maxDate: store.now, maxSpanDays: 31))
    self.picker = picker
    picker.onApply = { [weak self] range in
      guard let self else { return nil }
      self.store.perform(.dates(range.start, range.end))
      if self.store.error == nil { self.onApply?() }
      return self.store.error
    }
    picker.onCancel = { [weak self] in self?.onApply?() }
    picker.loadViewIfNeeded()
    view = picker.view
    preferredContentSize = picker.preferredContentSize
  }
}
