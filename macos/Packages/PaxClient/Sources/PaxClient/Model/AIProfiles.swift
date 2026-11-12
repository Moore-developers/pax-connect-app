import Foundation
import PaxProto

public enum AIClient: String, CaseIterable, Identifiable, Sendable {
  case claude = "Claude Code"
  case codex = "Codex"
  case harness = "Deepseek Harness"
  public var id: String { rawValue }
  public var code: String {
    switch self {
    case .claude: "claude"
    case .codex: "codex"
    case .harness: "dsh"
    }
  }
  public init?(code: String) {
    switch code {
    case "claude": self = .claude
    case "codex": self = .codex
    case "dsh": self = .harness
    default: return nil
    }
  }
  public var symbol: String {
    switch self {
    case .claude: "asterisk"
    case .codex: "terminal"
    case .harness: "fish"
    }
  }
}

public struct AIProviderModel: Equatable, Sendable {
  public let id: String
  public let owner: String
  public let name: String
  public let contextWindow: Int64
  public init(id: String, owner: String = "", name: String = "", contextWindow: Int64 = 0) {
    self.id = id
    self.owner = owner
    self.name = name
    self.contextWindow = contextWindow
  }
  init(_ p: Pax_V1_AIProviderModel) {
    id = p.id
    owner = p.owner
    name = p.displayName
    contextWindow = p.contextWindow
  }
}

/// Public profile metadata never contains the stored API key.
public struct AIProfile: Identifiable, Equatable, Sendable {
  public var id: String
  public var revision: UInt64
  public var connectionRevision: UInt64
  public var name: String
  public var endpoint: String
  public var multiplier: Double
  /// Per-model multipliers keyed by request model id; a model not listed pays `multiplier`.
  /// Saving sends the whole map, so a profile edit must carry it unchanged.
  public var modelMultipliers: [String: Double] = [:]
  public var modelIDs: Set<String>
  public var kind: String
  public var nativeCLI: AIClient?
  public var catalog: [AIProviderModel] = []
  public var modelsState = ""
  public var modelsEndpoint = ""
  public var modelsChecked: Date?
  public var modelsError = ""
  public var hasKey: Bool
  public init(
    id: String, name: String, endpoint: String, multiplier: Double,
    modelIDs: Set<String> = [], revision: UInt64 = 0, hasKey: Bool = false,
    connectionRevision: UInt64 = 0, kind: String = "", nativeCLI: AIClient? = nil
  ) {
    self.id = id
    self.name = name
    self.endpoint = endpoint
    self.multiplier = multiplier
    self.modelIDs = modelIDs
    self.revision = revision
    self.kind = kind
    self.nativeCLI = nativeCLI
    self.hasKey = hasKey
    self.connectionRevision = connectionRevision
  }
  init(_ p: Pax_V1_AIProfileSummary) {
    self.init(
      id: p.id, name: p.name, endpoint: p.baseURL, multiplier: p.multiplier,
      modelIDs: Set(p.modelIds), revision: p.revision, hasKey: p.hasKey_p,
      connectionRevision: p.connectionRevision, kind: p.kind, nativeCLI: AIClient(code: p.nativeCli)
    )
    catalog = p.catalog.map(AIProviderModel.init)
    modelsState = p.modelsState
    modelsEndpoint = p.modelsEndpoint
    modelsChecked = p.modelsChecked > 0 ? Date(timeIntervalSince1970: Double(p.modelsChecked)) : nil
    modelsError = p.modelsError
    modelMultipliers = p.modelMultipliers
  }
  var proto: Pax_V1_AIProfileSummary {
    .with {
      $0.id = id
      $0.name = name
      $0.baseURL = endpoint
      $0.multiplier = multiplier
      $0.modelMultipliers = modelMultipliers
      $0.modelIds = modelIDs.sorted()
      $0.revision = revision
    }
  }
}
public struct AIBinding: Equatable, Sendable {
  public var profileID: String
  public var state: String
  public var reason: String
  public var inspectionID: String
  public var templateRevision: UInt64
  public var endpoint: String
  public var targetPath: String
  public var requiresModel: Bool
  public init(
    profileID: String = "", state: String = "not_imported", reason: String = "",
    inspectionID: String = "", templateRevision: UInt64 = 0, endpoint: String = "",
    targetPath: String = "", requiresModel: Bool = false
  ) {
    self.profileID = profileID
    self.state = state
    self.reason = reason
    self.inspectionID = inspectionID
    self.endpoint = endpoint
    self.targetPath = targetPath
    self.requiresModel = requiresModel
    self.templateRevision = templateRevision
  }
}
public struct AIProfilesWorkspace: Sendable {
  public var profiles: [AIProfile] = []
  public var bindings: [AIClient: AIBinding] = [:]
  public init() {}
  init(_ p: Pax_V1_AIProfilesWorkspace) {
    profiles = p.profiles.map(AIProfile.init)
    for b in p.bindings {
      guard let cli = AIClient(code: b.cli) else { continue }
      bindings[cli] = AIBinding(
        profileID: b.profileID, state: b.state, reason: b.reason,
        inspectionID: b.inspectionID, templateRevision: b.templateRevision, endpoint: b.endpoint,
        targetPath: b.targetPath, requiresModel: b.requiresModel)
    }
  }
}
public struct AIProfilePlan: Sendable {
  public let id: String
  public let cli: AIClient
  public let profileID: String
  public let endpoint: String
  public let expires: Date
  public let initializes: Bool
  public let targetPath: String
  init(_ p: Pax_V1_AIProfilePlan) throws {
    guard let cli = AIClient(code: p.cli) else { throw ClientError.invalidEvent }
    initializes = p.initializes
    targetPath = p.targetPath
    id = p.id
    self.cli = cli
    profileID = p.profileID
    endpoint = p.baseURL
    expires = Date(timeIntervalSince1970: Double(p.expiresUnix))
  }
}
