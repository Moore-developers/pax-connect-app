import Foundation
import PaxClient
import Testing

@testable import PaxCore

final class FakeRefreshClock: @unchecked Sendable {
  private let lock = NSLock()
  private var time = Date(timeIntervalSince1970: 1_800_000_000)
  private var timers: [(id: Int, due: Date, action: @MainActor @Sendable () -> Void)] = []
  private var nextID = 0
  var clock: RefreshClock {
    RefreshClock(
      now: { [self] in lock.withLock { time } },
      after: { [self] delay, action in
        let id = lock.withLock { () -> Int in
          nextID += 1
          timers.append((nextID, time.addingTimeInterval(delay), action))
          return nextID
        }
        return { [self] in lock.withLock { timers.removeAll { $0.id == id } } }
      })
  }
  @MainActor func advance(_ seconds: TimeInterval) {
    let target = lock.withLock { time.addingTimeInterval(seconds) }
    while true {
      let next = lock.withLock { () -> (Int, Date, @MainActor @Sendable () -> Void)? in
        guard let t = timers.filter({ $0.due <= target }).min(by: { $0.due < $1.due }) else {
          return nil
        }
        timers.removeAll { $0.id == t.id }
        time = max(time, t.due)
        return (t.id, t.due, t.action)
      }
      guard let next else { break }
      next.2()
    }
    lock.withLock { time = target }
  }
}

final class ReportBox: @unchecked Sendable {
  private let lock = NSLock()
  private var items: [[String]] = []
  var all: [[String]] { lock.withLock { items } }
  func add(_ ids: [String]) { lock.withLock { items.append(ids) } }
}

@MainActor private final class Harness {
  let clock = FakeRefreshClock()
  var signals: [RefreshDomain: RefreshSignal] = [
    .traffic: .init(version: 1, generation: 0), .logs: .init(version: 1, generation: 0),
  ]
  let box = ReportBox()
  var reports: [[String]] { box.all }
  var fetches: [RefreshPage: Int] = [:]
  lazy var refresh = PaxRefresh(
    clock: clock.clock, signals: { [unowned self] in signals },
    report: { [box] ids in box.add(ids) })
  func bind(_ pages: RefreshPage...) {
    for page in pages { refresh.bind(page) { [unowned self] in fetches[page, default: 0] += 1 } }
  }
  func connect() {
    refresh.consume(
      .ready(
        .init(daemonVersion: "t", protocolVersion: 1, snapshotSeq: 1),
        DaemonSnapshot(
          snapshotVersion: 1, status: .off, routes: [], currentRoute: nil, todayTraffic: .zero,
          settings: DaemonSettings(autoConnect: false), recentLogs: [])))
  }
  func push() { refresh.consume(.events([])) }
}

@MainActor @Suite("PaxRefresh") struct PaxRefreshTests {
  @Test("可见才拉：隐藏页收到信号只记脏，进页即读")
  func fetchesOnlyWhileVisible() {
    let h = Harness()
    h.bind(.traffic)
    h.connect()
    h.signals[.traffic] = .init(version: 2)
    h.push()
    #expect(h.fetches[.traffic] == nil)
    h.refresh.pageDidAppear(.traffic)
    #expect(h.fetches[.traffic] == 1)
  }
  @Test("隐藏记脏，回到页面立即拉，不受最小间隔限制")
  func dirtyWhileWindowHidden() {
    let h = Harness()
    h.bind(.traffic)
    h.connect()
    h.refresh.pageDidAppear(.traffic)
    #expect(h.fetches[.traffic] == 1)
    h.refresh.setWindowVisible(false)
    h.signals[.traffic] = .init(version: 2)
    h.push()
    #expect(h.fetches[.traffic] == 1)
    h.clock.advance(1)
    h.refresh.setWindowVisible(true)
    #expect(h.fetches[.traffic] == 2)
  }
  @Test("最小间隔：间隔内的变化合并，到点只拉一次")
  func minimumInterval() {
    let h = Harness()
    h.bind(.logs)  // 5s
    h.connect()
    h.refresh.pageDidAppear(.logs)
    h.clock.advance(1)
    h.signals[.logs] = .init(version: 2)
    h.push()
    h.signals[.logs] = .init(version: 3)
    h.push()
    #expect(h.fetches[.logs] == 1)
    h.clock.advance(3.9)
    #expect(h.fetches[.logs] == 1)
    h.clock.advance(0.2)
    #expect(h.fetches[.logs] == 2)
    h.clock.advance(60)
    #expect(h.fetches[.logs] == 2)
  }
  @Test("作废代数变化绕过最小间隔")
  func generationBypassesInterval() {
    let h = Harness()
    h.bind(.traffic)  // 60s
    h.connect()
    h.refresh.pageDidAppear(.traffic)
    h.clock.advance(1)
    h.signals[.traffic] = .init(version: 2)
    h.push()
    #expect(h.fetches[.traffic] == 1)
    h.signals[.traffic] = .init(version: 3, generation: 1)
    h.push()
    #expect(h.fetches[.traffic] == 2)
  }
  @Test("断线不拉，重连首帧补拉并重报可见集合")
  func reconnectReportsAgain() async {
    let h = Harness()
    h.bind(.dashboard, .aiOverview)
    h.connect()
    h.refresh.pageDidAppear(.dashboard)
    h.refresh.pageDidAppear(.aiOverview)
    h.refresh.pageDidAppear(.routeDetail)  // not tracked by the daemon
    for _ in 0..<10 { await Task.yield() }
    #expect(h.reports.last == ["ai_overview", "dashboard"])
    let before = h.reports.count
    h.refresh.consume(.disconnected)
    h.signals[.traffic] = .init(version: 9)
    h.push()
    #expect(h.fetches[.dashboard] == 1)
    h.connect()
    for _ in 0..<10 { await Task.yield() }
    #expect(h.fetches[.dashboard] == 2)
    #expect(h.reports.count > before)
    #expect(h.reports.last == ["ai_overview", "dashboard"])
  }
  @Test("离页后上报集合收缩，窗口隐藏时上报空集")
  func reportsFollowVisibility() async {
    let h = Harness()
    h.connect()
    h.refresh.pageDidAppear(.logs)
    h.refresh.pageDidAppear(.traffic)
    h.refresh.pageDidDisappear(.logs)
    h.refresh.setWindowVisible(false)
    for _ in 0..<10 { await Task.yield() }
    #expect(h.reports.contains(["logs", "traffic"]))
    #expect(h.reports.contains(["traffic"]))
    #expect(h.reports.last == [])
  }
  @Test("日志响应事实驱动流量页，最小间隔内合并；删除立即补拉")
  func logsDriveRetainedTrafficResponses() {
    let h = Harness()
    h.bind(.traffic, .routeDetail)
    h.connect()
    h.refresh.pageDidAppear(.traffic)
    h.refresh.pageDidAppear(.routeDetail)
    h.signals[.logs] = .init(version: 2)
    h.push()
    #expect(h.fetches[.traffic] == 1)
    h.clock.advance(60)
    #expect(h.fetches[.traffic] == 2)
    #expect(h.fetches[.routeDetail] == 1)
    h.signals[.logs] = .init(version: 3, generation: 1)
    h.push()
    #expect(h.fetches[.traffic] == 3)
  }
  @Test("日志响应事实驱动概览延迟，10秒合并且删除立即补拉")
  func logsDriveDashboardResponses() {
    let h = Harness()
    h.bind(.dashboard)
    h.connect()
    h.refresh.pageDidAppear(.dashboard)
    h.signals[.logs] = .init(version: 2)
    h.push()
    #expect(h.fetches[.dashboard] == 1)
    h.clock.advance(9)
    #expect(h.fetches[.dashboard] == 1)
    h.clock.advance(1)
    #expect(h.fetches[.dashboard] == 2)
    h.signals[.logs] = .init(version: 3, generation: 1)
    h.push()
    #expect(h.fetches[.dashboard] == 3)
  }

  @Test("无关域的变化不触发页面")
  func unrelatedDomainIgnored() {
    let h = Harness()
    h.bind(.logs)
    h.connect()
    h.refresh.pageDidAppear(.logs)
    h.clock.advance(100)
    h.signals[.traffic] = .init(version: 5)
    h.push()
    #expect(h.fetches[.logs] == 1)
  }
}

@MainActor private final class AsyncRefreshReads {
  var count = 0
  var waits: [CheckedContinuation<Void, Never>] = []
  func read() async {
    count += 1
    await withCheckedContinuation { waits.append($0) }
  }
  func finish() { waits.removeFirst().resume() }
}

extension PaxRefreshTests {
  @Test("同页消费者按域独立限频；慢组在途通知只合并一个后继")
  func independentAsyncConsumers() async {
    let h = Harness()
    let slow = AsyncRefreshReads()
    var fast = 0
    h.refresh.bind(.dashboard, consumer: "today", domains: [.trafficData]) { fast += 1 }
    h.refresh.bind(.dashboard, consumer: "analysis", domains: [.traffic, .logs]) {
      await slow.read()
    }
    h.connect()
    h.refresh.pageDidAppear(.dashboard)
    for _ in 0..<20 { await Task.yield() }
    #expect(fast == 1 && slow.count == 1)
    h.clock.advance(10)
    h.signals[.logs] = .init(version: 2)
    h.push()
    h.signals[.logs] = .init(version: 3)
    h.push()
    #expect(fast == 1 && slow.count == 1)
    slow.finish()
    for _ in 0..<20 { await Task.yield() }
    #expect(slow.count == 2 && fast == 1)
    // A log deletion cannot consume the independent traffic-facts interval.
    h.signals[.logs] = .init(version: 4, generation: 1)
    h.push()
    h.signals[.trafficData] = .init(version: 2)
    h.push()
    for _ in 0..<20 { await Task.yield() }
    #expect(fast == 2)
    #expect(h.refresh.visibleViewIDs == ["dashboard"])
    while !slow.waits.isEmpty { slow.finish() }
    h.refresh.pageDidDisappear(.dashboard)
  }

  @Test("隐藏取消未完成组，恢复补读；旧completion不能清除新flight")
  func cancelledAsyncConsumerStaysDirty() async {
    let h = Harness()
    let reads = AsyncRefreshReads()
    h.refresh.bind(.dashboard, consumer: "today", domains: [.trafficData]) { await reads.read() }
    h.connect()
    h.refresh.pageDidAppear(.dashboard)
    for _ in 0..<20 { await Task.yield() }
    #expect(reads.count == 1)
    h.refresh.setWindowVisible(false)
    h.clock.advance(1)
    h.refresh.setWindowVisible(true)
    for _ in 0..<20 { await Task.yield() }
    #expect(reads.count == 2)
    reads.finish()  // Completes the cancelled request after its replacement has started.
    for _ in 0..<20 { await Task.yield() }
    h.clock.advance(10)
    h.signals[.trafficData] = .init(version: 2)
    h.push()
    for _ in 0..<20 { await Task.yield() }
    #expect(reads.count == 2)
    reads.finish()
    for _ in 0..<20 { await Task.yield() }
    #expect(reads.count == 3)
    reads.finish()
    h.refresh.pageDidDisappear(.dashboard)
  }
}
