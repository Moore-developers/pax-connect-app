import Foundation

public struct ObjectIconResult: Sendable, Equatable {
  public var host = ""
  public var png = Data()
  public var state = "missing"
  public var reason = ""
  public var expiresAt: Date?
  public var logDeletionGeneration: UInt64 = 0
  public init() {}
}
