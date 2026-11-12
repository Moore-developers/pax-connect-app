import Foundation

extension AIProfilesStore {
  public static func stateLabel(_ b: AIBinding?) -> String {
    guard let b else { return "未导入" }
    switch b.state {
    case "applied": return "配置已写入"
    case "pending": return "配置待应用"
    case "changed": return "配置已变更"
    case "ready": return "可导入配置"
    case "missing": return "未配置 API"
    case "account": return "检测到账号配置"
    case "invalid": return "配置需检查"
    case "read_only": return "只读用量"
    case "restored": return "已恢复原配置"
    case "recovery_required": return "需要恢复"
    default: return b.reason == "ai_template_unsupported" ? "配置暂不支持" : "未配置"
    }
  }
  static func message(_ error: any Error) -> String {
    if let failure = error as? ProductError {
      switch failure.code {
      case "ai_profile_invalid": return "请检查名称、API 地址、Key 和倍率"
      case "ai_profile_conflict": return "配置已更新，请刷新后重试"
      case "ai_profile_in_use": return "此 Profile 仍在使用，请先切换"
      case "ai_target_changed": return "CLI 配置刚被修改，请刷新后重试；未覆盖新内容"
      case "ai_template_unsupported": return "配置格式或认证位置不明确，未写入；请在客户端检查配置"
      case "ai_target_unsafe": return "配置路径或权限不符合要求，未修改文件"
      case "ai_plan_expired": return "切换预览已过期，请重新选择"
      case "ai_recovery_required", "ai_commit_uncertain": return "配置写入需要恢复，已停止后续操作"
      default: return "配置存储不可用，请检查文件或稍后重试"
      }
    }
    return (error as? ClientError)?.message ?? "配置服务暂不可用"
  }
}
