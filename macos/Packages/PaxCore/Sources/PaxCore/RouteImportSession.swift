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
  public private(set) var message = ""
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
    message = trimmed.isEmpty ? "" : "正在读取链接…"
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
    message = ""
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
      message = subscription ? "正在下载订阅…" : "正在读取链接…"
      let result = try await fetch(trimmed, subscription, name)
      guard current == generation && !Task.isCancelled else {
        await discard(result.token)
        return
      }
      preview = result
      previewInput = text
      previewName = name
      selected = Set(result.items.filter(Self.selectable).map(\.id))
      message = "\(selected.count) 条可导入；重复、隐藏与错误项不会导入"
    } catch {
      if current == generation {
        message = Self.describe(error)
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
        message = "线路已存在，未重复导入"
      } else {
        phase = .failed
        message = "没有可导入的线路，请查看下方原因"
      }
      await discard(result.token)
      return
    }
    busy = true
    committing = true
    phase = .importing
    message = isSource ? "正在创建或更新订阅分组…" : "正在导入 \(valid.count) 条线路…"
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
      let operation = isSource ? "订阅已导入或更新" : "已导入 \(valid.count) 条线路"
      message = operation + (errors.isEmpty ? "，正在测试延迟" : "；\(errors.count) 条失败，查看下方原因")
    } catch {
      phase = .failed
      message = Self.describe(error) + "。输入已保留；请核对列表后重试"
      preview = nil
      selected = []
      await discard(result.token)
    }
  }
  public func confirm() async {
    guard canConfirm, let preview else { return }
    busy = true
    committing = true
    message = "正在等待导入结果…"
    defer {
      busy = false
      committing = false
    }
    do {
      try await commit(preview, selected.sorted())
      finished = true
      self.preview = nil
      message = "导入完成"
    } catch {
      message = Self.describe(error) + "。输入已保留；请先核对目录，再重新预览"
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
  private static func describe(_ error: Error) -> String {
    if let error = error as? ProductError { return ProductErrorPresentation.message(error) }
    if let error = error as? LocalizedError, let description = error.errorDescription {
      return description
    }
    if let error = error as? ClientError { return error.message }
    return "预览或导入失败，请检查输入与后台连接"
  }
}
