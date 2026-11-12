import Foundation
import PaxCore

extension RouteWorkbenchStore {
  var canCreateProxy: Bool {
    canEdit && (routes?.capabilities.contains("routes.proxy.create") ?? false)
  }
  func createProxy(_ value: ProxyRouteDraft, requestID: String) async throws -> String {
    guard canCreateProxy, let routes else { throw EditError("当前后台不支持新增代理线路") }
    var edit = RouteEdit()
    edit.name = value.displayName
    edit.documentJSON = try value.document()
    edit.requestID = requestID
    guard let result = try await routes.saveDocument(edit), !result.routeID.isEmpty else {
      throw EditError("保存结果尚未确认，请重新连接后台后重试")
    }
    perform(.group("all"))
    perform(.search(""))
    message = result.created ? "已添加线路，尚未切换" : "此线路已存在，已定位"
    return result.routeID
  }
}
