import Testing

@testable import PaxCore

struct FeedbackTimelineTests {
  @Test("新错误优先；不变快照不重排；重试与内容变化重新置顶")
  func newestFirst() {
    var timeline = FeedbackTimeline()
    let proxy = ConnectionFeedback(id: "proxy", title: "代理失败", detail: "a", action: .details)
    let source = ConnectionFeedback(id: "source", title: "订阅失败", detail: "b", action: .details)
    #expect(timeline.update([proxy], attempts: [:]).map(\.id) == ["proxy"])
    #expect(timeline.update([proxy, source], attempts: [:]).map(\.id) == ["source", "proxy"])
    #expect(timeline.update([proxy, source], attempts: [:]).map(\.id) == ["source", "proxy"])
    #expect(timeline.update([proxy, source], attempts: ["proxy": 1]).first == proxy)
    let changed = ConnectionFeedback(id: "source", title: "订阅失败", detail: "c", action: .details)
    #expect(timeline.update([proxy, changed], attempts: ["proxy": 1]).first == changed)
    #expect(timeline.update([proxy], attempts: ["proxy": 1]) == [proxy])
  }
}
