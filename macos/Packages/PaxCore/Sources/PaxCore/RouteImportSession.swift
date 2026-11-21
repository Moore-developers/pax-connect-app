import Foundation
import Observation
import PaxClient

/// Short-lived import draft. Catalog facts remain in RoutesStore.
@MainActor @Observable
public final class RouteImportSession {
  public var source = ""
  public var sourceName = ""
  public private(set) var preview: RoutesPreview?
  public private(set) var selected: Set<String> = []
  public private(set) var busy = false
  public private(set) var committing = false
  public private(set) var finished = false
  private var retainedMessage = RetainedProductMessage()
  private var messageSuffix = RetainedProductMessage()
  public var message: String { message(context: .chinese) }
  public func message(context: PresentationContext) -> String {
    retainedMessage.text(context: context) + messageSuffix.text(context: context)
  }
  private func setMessage(
    _ message: RetainedProductMessage, suffix: RetainedProductMessage = .init()
  ) {
    retainedMessage = message
    messageSuffix = suffix
  }
  public enum Phase: Sendable {
    case idle, reading, downloading, importing, completed, partial, failed, cancelled
  }
  public private(set) var phase: Phase = .idle
  private var lastImportedInput = ""

  private var recognition: Task<Void, Never>?
  private var generation = 0
  private var previewInput = ""
  private var previewName = ""
  private let fetch: (String, Bool, String) async throws -> RoutesPreview
  private let discard: (String) async -> Void
  private let commit: (RoutesPreview, [String]) async throws -> Void

  public init(
    preview: @escaping (String, Bool, String) async throws -> RoutesPreview,
    cancel: @escaping (String) async -> Void,
    confirm: @escaping (RoutesPreview, [String]) async throws -> Void
  ) {
    fetch = preview
    discard = cancel
    commit = confirm
  }
  public convenience init(routes: RoutesStore) {
    self.init(
      preview: { try await routes.makePreview($0, subscription: $1, sourceName: $2) },
      cancel: { await routes.cancelPreview($0) },
      confirm: { try await routes.importAutomatically($0, items: $1) })
  }
  public var canConfirm: Bool {
    !busy && !finished && preview != nil && !selected.isEmpty
      && source == previewInput && sourceName == previewName
  }
  public static func selectable(_ item: RoutePreviewItem) -> Bool {
    item.route != nil && item.errorCode.isEmpty && !item.duplicate && !item.hidden
  }
  public func select(_ id: String, enabled: Bool) {
    guard !busy, let item = preview?.items.first(where: { $0.id == id }), Self.selectable(item)
    else { return }
    if enabled { selected.insert(id) } else { selected.remove(id) }
  }
  /// Coalesce typing; invalidate old results immediately, including in-flight fetches.
  public func recognize(_ text: String) {
    guard !committing else { return }
    let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
    guard trimmed != lastImportedInput || trimmed.isEmpty || trimmed.hasPrefix("https://") else {
      return
    }
    finished = false
    recognition?.cancel()
    generation += 1
    source = text
    busy = false
    let old = preview
    preview = nil
    selected = []
    setMessage(trimmed.isEmpty ? .init() : RetainedProductMessage(key: "route_import.reading"))
    phase = trimmed.isEmpty ? .idle : .reading
    recognition = Task { [weak self] in
      if let old { await self?.discard(old.token) }
      do { try await Task.sleep(for: .milliseconds(300)) } catch { return }
      guard let self, !Task.isCancelled,
        !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
      else { return }
      await self.makePreview()
      guard !Task.isCancelled else { return }
      await self.importRecognized()
    }
  }
  public func makePreview() async {
    guard !busy && !finished else { return }
    busy = true
    setMessage(.init())
    generation += 1
    let current = generation
    let text = source
    let name = sourceName
    let old = preview
    preview = nil
    selected = []
    if let old { await discard(old.token) }
    defer { if generation == current { busy = false } }
    do {
      let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
      let subscription = trimmed.hasPrefix("https://") && !trimmed.contains("\n")
      phase = subscription ? .downloading : .reading
      setMessage(
        subscription
          ? RetainedProductMessage(key: "route_import.downloading")
          : RetainedProductMessage(key: "route_import.reading"))
      let result = try await fetch(subscription ? trimmed : text, subscription, name)
      guard current == generation && !Task.isCancelled else {
        await discard(result.token)
        return
      }
      preview = result
      previewInput = text
      previewName = name
      selected = Set(result.items.filter(Self.selectable).map(\.id))
      setMessage(
        RetainedProductMessage(
          key: "route_import.eligible", parameters: ["count": String(selected.count)]))
    } catch {
      if current == generation {
        setMessage(Self.failureMessage(error))
        phase = .failed
      }
    }
  }
  private func importRecognized() async {
    guard let result = preview, !committing, !finished else { return }
    let valid = result.items.filter(Self.selectable)
    let errors = result.items.filter { !$0.errorCode.isEmpty }
    let duplicates = result.items.filter { $0.duplicate || $0.hidden }
    let isSource = !result.sourceHost.isEmpty
    let hasValidSource = result.items.contains { $0.route != nil && $0.errorCode.isEmpty }
    if valid.isEmpty && !(isSource && hasValidSource) {
      if errors.isEmpty && !duplicates.isEmpty {
        lastImportedInput = source.trimmingCharacters(in: .whitespacesAndNewlines)
        finished = true
        phase = .completed
        setMessage(RetainedProductMessage(key: "route_import.duplicate"))
      } else {
        phase = .failed
        setMessage(RetainedProductMessage(key: "route_import.none"))
      }
      await discard(result.token)
      return
    }
    busy = true
    committing = true
    phase = .importing
    setMessage(
      isSource
        ? RetainedProductMessage(key: "route_import.source_importing")
        : RetainedProductMessage(
          key: "route_import.importing", parameters: ["count": String(valid.count)]))
    defer {
      busy = false
      committing = false
    }
    do {
      try Task.checkCancellation()
      try await commit(result, valid.map(\.id))
      lastImportedInput = source.trimmingCharacters(in: .whitespacesAndNewlines)
      finished = true
      phase = errors.isEmpty ? .completed : .partial
      setMessage(
        isSource
          ? RetainedProductMessage(key: "route_import.source_imported")
          : RetainedProductMessage(
            key: "route_import.imported", parameters: ["count": String(valid.count)]),
        suffix: errors.isEmpty
          ? RetainedProductMessage(key: "route_import.testing_suffix")
          : RetainedProductMessage(
            key: "route_import.partial_suffix", parameters: ["count": String(errors.count)]))
    } catch {
      phase = .failed
      setMessage(Self.failureMessage(error), suffix: .init(key: "route_import.failed_retry"))
      preview = nil
      selected = []
      await discard(result.token)
    }
  }
  public func confirm() async {
    guard canConfirm, let preview else { return }
    busy = true
    committing = true
    setMessage(RetainedProductMessage(key: "route_import.waiting"))
    defer {
      busy = false
      committing = false
    }
    do {
      try await commit(preview, selected.sorted())
      finished = true
      self.preview = nil
      setMessage(RetainedProductMessage(key: "route_import.complete"))
    } catch {
      setMessage(Self.failureMessage(error), suffix: .init(key: "route_import.failed_preview"))
      // An ACK may have committed before disconnect. Never resubmit this token automatically.
      self.preview = nil
      selected = []
      await discard(preview.token)
    }
  }
  /// Revoke submission synchronously when a window closes. Token disposal is I/O
  /// and may finish later, but an already queued response has lost its generation.
  public func cancelImmediately() {
    guard !committing else { return }
    let old = invalidateRecognition()
    if let old { Task { [discard] in await discard(old.token) } }
  }
  public func cancel() async {
    guard !committing else { return }
    let old = invalidateRecognition()
    if let old { await discard(old.token) }
  }
  private func invalidateRecognition() -> RoutesPreview? {
    recognition?.cancel()
    generation += 1
    busy = false
    phase = .cancelled
    let old = preview
    preview = nil
    selected = []
    return old
  }
  private static func failureMessage(_ error: Error) -> RetainedProductMessage {
    guard let failure = failure(error) else {
      return RetainedProductMessage(key: "route_import.failed_unknown")
    }
    return RetainedProductMessage(failure: failure)
  }
  private static func failure(_ error: Error) -> ProductFailure? {
    if error is ProductError || error is ClientError { return ProductFailure(error) }
    if let error = error as? LocalizedError, let description = error.errorDescription {
      return .original(description)
    }
    return nil
  }
}
