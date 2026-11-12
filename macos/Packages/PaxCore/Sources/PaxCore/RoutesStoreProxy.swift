import PaxClient

extension RoutesStore {
  @discardableResult public func saveDocument(_ edit: RouteEdit) async throws -> RouteSaveResult? {
    try await performAndWait(.save(edit)).routeSave
  }
}
