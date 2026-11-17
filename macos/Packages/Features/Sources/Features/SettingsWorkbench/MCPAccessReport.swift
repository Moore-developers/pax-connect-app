import Foundation

/// Presentation-only installation facts; never carries native configuration or credentials.
public struct MCPAccessReport: Decodable, Sendable {
  public struct Client: Decodable, Sendable {
    public let id: String
    public let name: String
    public let state: String
    public let entryName: String
    public let permissions: String
  }
  public let entry: String
  public let clients: [Client]
  public let verified: Bool
  public let error: String
}

/// App owns installation I/O; Features requests explicit inspect/connect/verify intents.
public struct MCPAccessActions: Sendable {
  public let run:
    @MainActor @Sendable (String, String, String, String) async throws -> MCPAccessReport

  public init(
    run:
      @escaping @MainActor @Sendable (String, String, String, String) async throws ->
      MCPAccessReport
  ) {
    self.run = run
  }
}
