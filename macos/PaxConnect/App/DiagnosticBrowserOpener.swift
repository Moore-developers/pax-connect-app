import AppKit

/// LaunchServices' nonactivating option is advisory: Chrome may still activate on a URL event.
@MainActor final class DiagnosticBrowserOpener {
  private var activationObserver: NSObjectProtocol?
  private var expiry: Task<Void, Never>?

  func open(_ url: URL) async -> Bool {
    stopPreservingFocus()
    guard let browser = NSWorkspace.shared.urlForApplication(toOpen: url) else { return false }
    let workspace = NSWorkspace.shared
    if workspace.frontmostApplication?.processIdentifier
      == ProcessInfo.processInfo.processIdentifier
    {
      activationObserver = workspace.notificationCenter.addObserver(
        forName: NSWorkspace.didActivateApplicationNotification, object: nil, queue: .main
      ) { [weak self] notification in
        let app =
          notification.userInfo?[NSWorkspace.applicationUserInfoKey] as? NSRunningApplication
        let pid = app?.processIdentifier
        let bundleURL = app?.bundleURL
        MainActor.assumeIsolated {
          guard let self, let pid, pid != ProcessInfo.processInfo.processIdentifier
          else { return }
          self.stopPreservingFocus()
          // Leave an unrelated app alone if the user changes tasks during opening.
          guard bundleURL == browser else { return }
          NSRunningApplication.current.activate()
        }
      }
      expiry = Task { [weak self] in
        // Actual Chrome activation was observed within 0.6s; never keep a standing focus guard.
        do { try await Task.sleep(for: .seconds(2)) } catch { return }
        self?.stopPreservingFocus()
      }
    }
    let configuration = NSWorkspace.OpenConfiguration()
    configuration.activates = false
    let opened = await withCheckedContinuation { continuation in
      workspace.open(url, configuration: configuration) { _, error in
        continuation.resume(returning: error == nil)
      }
    }
    if !opened || Task.isCancelled { stopPreservingFocus() }
    return opened
  }

  func stopPreservingFocus() {
    if let activationObserver {
      NSWorkspace.shared.notificationCenter.removeObserver(activationObserver)
    }
    activationObserver = nil
    expiry?.cancel()
    expiry = nil
  }
}
