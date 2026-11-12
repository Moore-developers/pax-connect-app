import AppKit

/// 用量所属的 CLI 种类；PaxUI 不依赖 PaxClient，由调用方传入名称。
public enum PaxClientKind: Equatable, Sendable {
  case claudeCode, codex, dsh, unknown

  /// 容忍大小写、空格、连字符：`Claude Code`、`claude-code`、`Codex`、`DSH`、`Deepseek Harness` 等。
  public init(name: String) {
    let key = name.lowercased().filter { $0.isLetter || $0.isNumber }
    if key.contains("claude") {
      self = .claudeCode
    } else if key.contains("codex") {
      self = .codex
    } else if key == "dsh" || key.contains("harness") || key.contains("deepseek") {
      self = .dsh
    } else {
      self = .unknown
    }
  }
}

extension PaxTheme.Chart {
  /// 按 CLI 种类而非下标取色：Claude Code = 输入橙、Codex = 品牌蓝、DSH = 品牌紫，未知回落到次级文字灰。
  public static func clientSeries(for kind: PaxClientKind) -> NSColor {
    switch kind {
    case .claudeCode: return inputSeries
    case .codex: return codexSeries
    case .dsh: return deepSeekSeries
    case .unknown: return PaxTheme.Color.textSecondary
    }
  }
  public static func clientSeries(for name: String) -> NSColor {
    clientSeries(for: PaxClientKind(name: name))
  }
}
