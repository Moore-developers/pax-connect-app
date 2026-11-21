import AppKit
import PaxCore
import PaxUI

extension AIUsageViewController {
  func showConfiguration() { store.perform(.page(.profiles)) }
  /// Useful even after guidance is dismissed: a period with no requests is not a missing setup.
  func usageGettingStarted() -> NSView {
    let note = PaxInlineStatus()
    note.onAction = { [weak self] in
      guard let self, let last = self.store.data?.lastUsed.values.max() else { return }
      self.store.perform(.dates(last, last))
    }
    let update: () -> Void = { [weak self, weak note] in
      guard let self, let note else { return }
      let ready =
        !store.preview && store.data?.bucketsLoaded == true && store.data?.error == nil
        && store.data?.state.errorCode.isEmpty == true && store.data?.state.backfilling != true
      note.isHidden = !ready || store.hasUsage || store.page == .profiles || store.page == .sessions
      if let last = store.data?.lastUsed.values.max() {
        note.apply(
          .idle,
          text: self.interfaceCopy.text("ai_usage.no_recent_requests_prefix")
            + last.formatted(date: .numeric, time: .omitted),
          action: self.interfaceCopy.text("ai_usage.view_recent_day"))
      } else {
        note.apply(.idle, text: self.interfaceCopy.text("ai_usage.no_sessions"))
      }
    }
    contentRefreshers.append(update)
    update()
    return note
  }
}
