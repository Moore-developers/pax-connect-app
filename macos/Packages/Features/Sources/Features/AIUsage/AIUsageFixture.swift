import Foundation
import PaxCore

/// Only the isolated workbench supplies these fixtures; the product entry uses an empty store.
enum AIUsageFixture {
  static let models: [AIModel] = [
    .init(
      id: "opus", name: "Claude Opus 4.6", clients: [.claude, .harness], inputPrice: 5,
      outputPrice: 25),
    .init(
      id: "sonnet", name: "Claude Sonnet 4.6", clients: [.claude, .harness], inputPrice: 3,
      outputPrice: 15),
    .init(
      id: "gpt", name: "GPT-5.4", clients: [.codex, .harness], inputPrice: 2.5, outputPrice: 15),
    .init(
      id: "gemini", name: "Gemini 2.5 Pro", clients: [.harness], inputPrice: 1.25, outputPrice: 10),
    .init(
      id: "deepseek", name: "DeepSeek V3", clients: [.codex, .harness], inputPrice: 0.3,
      outputPrice: 1.2),
    .init(
      id: "haiku", name: "Claude Haiku 4.5", clients: [.claude, .harness], inputPrice: 1,
      outputPrice: 5),
    .init(
      id: "gpt-mini", name: "GPT-5 mini", clients: [.codex, .harness], inputPrice: 0.25,
      outputPrice: 2),
    .init(
      id: "gemini-flash", name: "Gemini 2.5 Flash", clients: [.harness], inputPrice: 0.3,
      outputPrice: 2.5),
    .init(
      id: "qwen", name: "Qwen3 Coder", clients: [.codex, .harness], inputPrice: 0.3,
      outputPrice: 1.5),
    .init(
      id: "deepseek-r1", name: "DeepSeek R1", clients: [.harness], inputPrice: 0.55,
      outputPrice: 2.19),
  ]
  static let profiles: [AIProfile] = [
    .init(
      id: "krill", name: "Krill", endpoint: "https://api.krill.example/v1", multiplier: 0.3,
      modelIDs: Set(models.map(\.id))),
    .init(
      id: "anthropic", name: "Anthropic", endpoint: "https://api.anthropic.com", multiplier: 1,
      modelIDs: ["opus", "sonnet", "haiku"]),
    .init(
      id: "openai", name: "OpenAI", endpoint: "https://api.openai.com/v1", multiplier: 1,
      modelIDs: ["gpt", "gpt-mini"]),
  ]
  static let bindings: [AIClient: AIBinding] = [
    .claude: .init(profileID: "krill", state: "applied"),
    .codex: .init(profileID: "openai", state: "applied"),
    .harness: .init(profileID: "krill", state: "applied"),
  ]
  static func requests(now: Date, calendar: Calendar) -> [AIRequest] {
    let today = calendar.startOfDay(for: now)
    return (0..<30).flatMap { day in
      (0..<40).map { index in
        let model = models[index % models.count]
        let provider = profiles.filter { $0.modelIDs.contains(model.id) }[
          (index / 10) % profiles.filter { $0.modelIDs.contains(model.id) }.count]
        let success = (index + day) % 17 != 0
        let input = success ? 20_000 + index * 790 : 0
        let at = (calendar.date(byAdding: .day, value: -day, to: today) ?? today)
          .addingTimeInterval(Double(index) * 1_400)
        return AIRequest(
          id: "ai-\(day)-\(index)", at: at, profileID: provider.id, profileName: provider.name,
          modelID: model.id, client: model.clients.sorted { $0.rawValue < $1.rawValue }[0],
          succeeded: success, input: input, read: input * 8 / 10, write: input / 40,
          output: success ? 900 + index * 57 : 0, inputPrice: model.inputPrice,
          outputPrice: model.outputPrice, multiplier: provider.multiplier,
          tps: 20 + Double(index % 55),
          elapsed: success ? Double(900 + index * 57) / (20 + Double(index % 55)) : 2.5,
          errorMessage: success ? nil : "HTTP 502 · 上游错误")
      }.filter { $0.at <= now }
    }.sorted { $0.at > $1.at }
  }
}
