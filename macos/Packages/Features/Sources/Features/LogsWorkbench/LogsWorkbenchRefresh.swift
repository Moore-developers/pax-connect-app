import PaxCore

extension LogsWorkbenchViewController {
  func syncRefresh() {
    let visible =
      store.telemetry?.isShown(.logs, focus: pageRefresh == nil) == true
      && store.telemetry?.available == true && pageVisible
    if !visible {
      latestPending = false
      latestPendingGeneration = nil
      loadTask?.cancel()
      loadTask = nil
      detailTask?.cancel()
      detailTask = nil
      detailGeneration &+= 1
      store.stopQueries()
      return
    }
    if store.privacyInvalid && !store.isLoading && store.liveMessage.isEmpty {
      fetchLatest()
      return
    }
    if latestPending,
      !store.followsLatestPage || latestPendingGeneration != store.requestGeneration
    {
      latestPending = false
      latestPendingGeneration = nil
    }
    if latestPending, pageRefresh != nil, !store.pageLoading, !store.isLoading {
      fetchLatest()
      return
    }
    guard pageRefresh == nil, loadTask == nil else { return }
    startLatestRead()
  }
  /// PaxRefresh 的拉取：进页回到最新；停在第 1 页随日志推送换最新，其他页冻结快照。
  func fetchLatest() {
    guard let telemetry = store.telemetry, telemetry.isShown(.logs, focus: false),
      telemetry.available
    else { return }
    let invalid = store.privacyInvalid
    let entry = entryPending
    if !entry, !invalid, store.followsLatestPage, store.pageLoading || store.isLoading {
      // The user's target can be page zero while pageIndex still describes the old page.
      latestPending = true
      latestPendingGeneration = store.requestGeneration
      return
    }
    guard entry || invalid || store.followsLatestPage else { return }
    entryPending = false
    latestPending = false
    latestPendingGeneration = nil
    loadTask?.cancel()
    startLatestRead(entering: entry)
  }
  private func startLatestRead(entering: Bool = false) {
    loadTask = Task { [weak self] in
      guard let self else { return }
      await self.store.updateSnapshotOnEntry(entering: entering)
    }
  }
}
