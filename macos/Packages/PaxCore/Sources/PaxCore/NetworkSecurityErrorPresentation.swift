import PaxClient

extension ProductErrorPresentation {
  static func networkSecurityMessage(_ error: ProductError) -> String? {
    switch error.code {
    case "environment_browser_open_failed": "无法打开浏览器，已保留 Mac 检测结果；请确认默认浏览器可用"
    case "measurement_busy": "已有延迟或线路测量正在执行，请稍后重试"
    case "path_diagnostics_busy": "已有诊断正在执行，请等待结束或停止后重试"
    case "tls_unverified": "未校验服务器证书"
    case "tls_certificate_invalid":
      switch error.messageParams.first {
      case "expired": "服务器证书已过期或尚未生效"
      case "hostname_mismatch": "服务器证书与SNI名称不匹配"
      case "untrusted": "服务器证书签发链不受信任"
      case "noncompliant": "服务器证书不符合校验标准"
      default: "服务器证书校验失败，请检查证书或线路证书设置"
      }
    case "tls_confirmation_required": "请确认这次手动修改的证书设置"
    case "tls_policy_stale": "线路身份或设置已变化，请重新核对后保存"
    case "tls_policy_store_invalid": "线路校验设置文件损坏或权限不安全"
    case "tls_policy_save_failed": "线路校验设置保存失败，原设置未改变"
    case "network_privacy_invalid": "网络保护设置无效或已损坏，请检查本地配置"
    case "network_privacy_unavailable": "网络保护设置不可用，请重新读取；请勿直接覆盖损坏文件"
    case "network_privacy_conflict": "设置已被其它窗口修改，请重新读取后保存"
    case "network_privacy_save_failed": "网络保护设置保存失败，请重新读取确认当前版本"
    default: nil
    }
  }
}
