import Foundation
import Testing

@testable import PaxCore

@MainActor struct RouteImportSessionTests {
  @Test("只默认勾选有效项，输入变化后必须重新预览")
  func selectionsAndChangedInput() async {
    let route = NodeProfile(
      id: "r", name: "One", protocolName: "vless", server: "example.test", port: 443, group: "")
    var committed: [String] = []
    var cancelled: [String] = []
    let session = RouteImportSession(
      preview: { _, _, _ in
        RoutesPreview(
          token: "token", revision: 1,
          items: [
            RoutePreviewItem(id: "valid", route: route, errorCode: "", duplicate: false, line: 1),
            RoutePreviewItem(
              id: "duplicate", route: route, errorCode: "", duplicate: true, line: 2),
            RoutePreviewItem(
              id: "bad", route: nil, errorCode: "unsupported_link", duplicate: false, line: 3),
          ])
      }, cancel: { cancelled.append($0) }, confirm: { _, items in committed = items })
    session.source = "input"
    await session.makePreview()
    #expect(session.selected == ["valid"])
    session.source = "changed"
    #expect(!session.canConfirm)
    await session.confirm()
    #expect(committed.isEmpty)
    await session.cancel()
    #expect(cancelled == ["token"])
    #expect(!session.finished)
  }
}

private actor PreviewGate {
  private var continuation: CheckedContinuation<Void, Never>?
  private(set) var entered = false
  func wait() async {
    await withCheckedContinuation {
      continuation = $0
      entered = true
    }
  }
  func release() {
    continuation?.resume()
    continuation = nil
  }
}
extension RouteImportSessionTests {
  @Test("取消后的迟到预览释放token，输入仍保留")
  func latePreviewAfterCancellation() async {
    let gate = PreviewGate()
    var discarded: [String] = []
    let session = RouteImportSession(
      preview: { _, _, _ in
        await gate.wait()
        return RoutesPreview(token: "late", revision: 1, items: [])
      }, cancel: { discarded.append($0) }, confirm: { _, _ in Issue.record("取消不能确认") })
    session.source = "draft"
    let task = Task { await session.makePreview() }
    while !(await gate.entered) { await Task.yield() }
    await session.cancel()
    await gate.release()
    await task.value
    #expect(discarded == ["late"])
    #expect(session.preview == nil)
    #expect(session.source == "draft")
    #expect(!session.busy)
  }
  @Test("失败确认保留输入并禁止自动重试旧token")
  func failedCommit() async throws {
    let route = NodeProfile(
      id: "r", name: "One", protocolName: "vless", server: "example.test", port: 443, group: "")
    var calls = 0
    let session = RouteImportSession(
      preview: { _, _, _ in
        RoutesPreview(
          token: "token", revision: 1,
          items: [
            RoutePreviewItem(id: "valid", route: route, errorCode: "", duplicate: false, line: 1)
          ])
      }, cancel: { _ in },
      confirm: { _, _ in
        calls += 1
        throw ClientError.unavailable
      })
    session.source = "draft"
    await session.makePreview()
    await session.confirm()
    await session.confirm()
    #expect(calls == 1)
    #expect(!session.finished)
    #expect(!session.canConfirm)
    #expect(session.source == "draft")
    #expect(session.message.contains("核对目录"))
    let pack = try #require(LanguageCatalog.bundled().packs[.english])
    let english = PresentationContext(language: .english, messages: pack.messages)
    #expect(
      session.message(context: english).contains("Check the catalog before previewing again."))
  }
}

extension RouteImportSessionTests {
  @Test("自动识别仅发布最后输入，取消不入库")
  func automaticRecognition() async {
    var inputs: [String] = []
    let (recognized, didRecognize) = AsyncStream.makeStream(of: Void.self)
    defer { didRecognize.finish() }
    let session = RouteImportSession(
      preview: { source, _, name in
        inputs.append(source)
        #expect(name.isEmpty)
        didRecognize.yield()
        return RoutesPreview(token: source, revision: 1, items: [])
      }, cancel: { _ in }, confirm: { _, _ in Issue.record("识别不应入库") })
    session.recognize("first")
    session.recognize("last")
    var pending = recognized.makeAsyncIterator()
    await pending.next()
    #expect(inputs == ["last"])
    #expect(session.preview?.token == "last")
    session.recognize("")
    #expect(session.preview == nil)
    await session.cancel()
  }
}

extension RouteImportSessionTests {
  @Test("粘贴自动提交有效项，错误保留且重复输入不重发")
  func pasteImportsWithoutConfirmation() async {
    let route = NodeProfile(
      id: "r", name: "Fixture", protocolName: "trojan", server: "example.test", port: 443, group: ""
    )
    var writes = 0
    let (commits, didCommit) = AsyncStream.makeStream(of: Void.self)
    defer { didCommit.finish() }
    let session = RouteImportSession(
      preview: { _, _, _ in
        RoutesPreview(
          token: "t", revision: 1,
          items: [
            RoutePreviewItem(id: "ok", route: route, errorCode: "", duplicate: false, line: 1),
            RoutePreviewItem(
              id: "bad", route: nil, errorCode: "unsupported_protocol", duplicate: false, line: 2),
          ])
      }, cancel: { _ in },
      confirm: { _, ids in
        writes += 1
        didCommit.yield()
        #expect(ids == ["ok"])
      })
    session.recognize("trojan://fixture@example.test:443\nunknown://bad")
    var pending = commits.makeAsyncIterator()
    await pending.next()
    #expect(writes == 1)
    #expect(session.finished)
    #expect(session.message.contains("1"))
    #expect(session.preview?.items.count == 2)
    session.recognize("trojan://fixture@example.test:443\nunknown://bad")
    try? await Task.sleep(for: .milliseconds(400))
    #expect(writes == 1)
  }
}

extension RouteImportSessionTests {
  @Test("自动下载期间换输入或取消，旧结果不能入库")
  func automaticLateResultDoesNotImport() async {
    let gate = PreviewGate()
    let route = NodeProfile(
      id: "r", name: "Old", protocolName: "trojan", server: "example.test", port: 443, group: "")
    var committed = 0
    let (discarded, didDiscard) = AsyncStream.makeStream(of: String.self)
    defer { didDiscard.finish() }
    let session = RouteImportSession(
      preview: { _, _, _ in
        await gate.wait()
        return RoutesPreview(
          token: "late", revision: 1,
          items: [
            RoutePreviewItem(id: "ok", route: route, errorCode: "", duplicate: false, line: 1)
          ])
      }, cancel: { didDiscard.yield($0) }, confirm: { _, _ in committed += 1 })
    session.recognize("https://example.test/sub")
    while !(await gate.entered) { await Task.yield() }
    #expect(session.phase == .downloading)
    await session.cancel()
    await gate.release()
    #expect(committed == 0)
    var pending = discarded.makeAsyncIterator()
    #expect(await pending.next() == "late")
  }
  @Test("再次粘贴同来源的重复项仍自动更新，不要求手动确认")
  func duplicateSourceStillUpdates() async {
    let route = NodeProfile(
      id: "r", name: "Existing", protocolName: "trojan", server: "example.test", port: 443,
      group: "")
    var updates = 0
    let (commits, didCommit) = AsyncStream.makeStream(of: Void.self)
    defer { didCommit.finish() }
    let session = RouteImportSession(
      preview: { _, _, _ in
        RoutesPreview(
          token: "source", revision: 1,
          items: [
            RoutePreviewItem(id: "existing", route: route, errorCode: "", duplicate: true, line: 1)
          ], sourceHost: "example.test", sourceName: "example.test", subscriptionID: "source")
      }, cancel: { _ in },
      confirm: { _, ids in
        updates += 1
        didCommit.yield()
        #expect(ids.isEmpty)
      })
    session.recognize("https://example.test/sub")
    var pending = commits.makeAsyncIterator()
    await pending.next()
    #expect(updates == 1)
    #expect(session.finished)
    session.recognize("https://example.test/sub")
    await pending.next()
    #expect(updates == 2)
  }
}

extension RouteImportSessionTests {
  @Test("关闭同步撤销提交资格，响应已返回也不能自动入库")
  func synchronousCloseRevokesReadyResponse() async {
    let route = NodeProfile(
      id: "r", name: "Fixture", protocolName: "trojan", server: "example.test", port: 443, group: ""
    )
    var session: RouteImportSession?
    var commits = 0
    let (discarded, didDiscard) = AsyncStream.makeStream(of: String.self)
    defer { didDiscard.finish() }
    session = RouteImportSession(
      preview: { _, _, _ in
        session?.cancelImmediately()
        return RoutesPreview(
          token: "ready", revision: 1,
          items: [
            RoutePreviewItem(id: "ok", route: route, errorCode: "", duplicate: false, line: 1)
          ])
      }, cancel: { didDiscard.yield($0) }, confirm: { _, _ in commits += 1 })
    session?.recognize("trojan://fixture@example.test:443")
    #expect(commits == 0)
    var pending = discarded.makeAsyncIterator()
    #expect(await pending.next() == "ready")
    #expect(session?.phase == .cancelled)
  }
}
