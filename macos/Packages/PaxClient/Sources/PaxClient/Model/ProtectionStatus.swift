/// 保护状态（与 `api/pax.proto` 的 `ProtectionStatus`、Go 端同名类型一字不差）。
///
/// 只有 `.protected` 允许 UI 显示「已保护」；`.degraded` 必须带原因（decisions/geo-dns-protection.md）。
public enum ProtectionStatus: Sendable, Equatable {
  case off
  case starting
  case protected
  case degraded
}
