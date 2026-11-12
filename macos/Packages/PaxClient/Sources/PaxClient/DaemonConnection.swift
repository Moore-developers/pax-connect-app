import Foundation
import GRPCCore
import GRPCNIOTransportHTTP2Posix
import PaxProto

/// 与 `pax-connectd` 的唯一连接通道（actor：不碰主线程）。
///
/// 传输方式已由 S0-③ 实测确认：Unix domain socket + 明文 HTTP/2（h2c，不带 TLS）。
/// 本类只做连接与映射；重连退避、事件聚合（250ms 批次）与断线保留快照由 ConnectionSession 管理。
public actor DaemonConnection {
  /// 产品协议版本；与 api/pax.proto 的语义一起演进，握手不一致时 UI 提示升级。
  public static let protocolVersion: UInt32 = 46
  /// 生产环境的 socket 路径（daemon 以 root 运行，目录 root:admin 0750、socket 0660）。
  public static let defaultSocketPath = "/var/run/pax-connect/pax.sock"

  let clientID = UUID().uuidString
  var socketPath: String
  let expectedOwnerPID: UInt32?
  let expectedFingerprint: String?
  let clientVersion: String

  public init(
    socketPath: String, clientVersion: String? = nil,
    expectedOwnerPID: UInt32? = nil, expectedFingerprint: String? = nil
  ) {
    self.expectedOwnerPID = expectedOwnerPID
    self.expectedFingerprint = expectedFingerprint
    self.socketPath = socketPath
    self.clientVersion = clientVersion ?? Self.bundleVersion
  }

  /// App resolves the canonical socket before starting/recovering the single session.
  public func useSocket(_ path: String) { socketPath = path }

  /// 一次连接：首帧同步快照确认后才 ready；会话负责退避和排序。
  public func run(
    onReady: @escaping @Sendable (DaemonHandshake, DaemonSnapshot) async throws -> Void = { _, _ in
    },
    onEvent: @escaping @Sendable (DaemonEventEnvelope) async throws -> Void
  ) async throws {
    try await withGRPCClient(transport: try Self.makeTransport(socketPath: socketPath)) { client in
      let service = Pax_V1_PaxService.Client(wrapping: client)
      var options = CallOptions.defaults
      options.timeout = .seconds(5)
      let hello = try await service.hello(
        .with {
          $0.clientVersion = self.clientVersion
          $0.protocolVersion = Self.protocolVersion
        },
        options: options)
      try self.validateIdentity(hello)
      let snapshot = try await service.getSnapshot(.init(), options: options)
      let handshake = DaemonHandshake(
        daemonVersion: hello.daemonVersion,
        protocolVersion: hello.protocolVersion, snapshotSeq: hello.snapshotSeq,
        serviceFingerprint: hello.serviceFingerprint, capabilities: Set(hello.capabilities),
        initialInstall: hello.initialInstall, ownerAppPID: hello.ownerAppPid)
      let boundary = SubscriptionBoundary()
      try await withThrowingTaskGroup(of: Void.self) { group in
        group.addTask {
          try await service.watch(.with { $0.fromSeq = snapshot.snapshotVersion }) { response in
            var first = true
            for try await proto in response.messages {
              try Task.checkCancellation()
              guard let event = DaemonEvent(proto: proto) else { throw ClientError.invalidEvent }
              if first {
                guard case .snapshot(let full) = event,
                  proto.seq == full.snapshotVersion, proto.snapshotVersion == full.snapshotVersion
                else { throw ClientError.invalidEvent }
                await boundary.confirm()
                try await onReady(handshake, full)
                first = false
              } else {
                try await onEvent(
                  DaemonEventEnvelope(
                    seq: proto.seq,
                    snapshotVersion: proto.snapshotVersion, event: event))
              }
            }
            throw ClientError.unavailable
          }
        }
        group.addTask {
          try await Task.sleep(for: .seconds(5))
          if await !boundary.confirmed { throw ClientError.timeout }
        }
        defer { group.cancelAll() }
        while try await group.next() != nil {}
      }
    }
  }

  /// Fresh authoritative snapshot before a destructive service update.
  public func getSnapshot() async throws -> DaemonSnapshot {
    try await withAuthenticatedClient { client in
      var options = CallOptions.defaults
      options.timeout = .seconds(5)
      return DaemonSnapshot(
        proto: try await Pax_V1_PaxService.Client(wrapping: client).getSnapshot(
          .init(), options: options))
    }
  }

  public func updateRouteTLS(_ intent: RouteTLSIntent) async throws -> String {
    guard intent.mode != .unknown else { throw ClientError.invalidEvent }
    return try await withAuthenticatedClient {
      client in
      var options = CallOptions.defaults
      options.timeout = .seconds(5)
      do {
        let ack = try await Pax_V1_PaxService.Client(wrapping: client).updateRouteTLS(
          .with {
            $0.routeID = intent.routeID
            $0.identityToken = intent.identityToken
            $0.expectedRevision = intent.expectedRevision
            $0.confirmSkip = intent.confirmSkip
            $0.mode = intent.mode == .skipVerify ? .skipVerify : .strict
          }, options: options)
        return ack.operationID
      } catch { throw ClientError.classify(error) }
    }
  }

  /// ACK confirms only admission of the operation; state comes from Watch.
  public func setProxyEnabled(_ enabled: Bool) async throws -> String {
    try await withAuthenticatedClient { client in
      var options = CallOptions.defaults
      options.timeout = .seconds(5)
      do {
        let ack = try await Pax_V1_PaxService.Client(wrapping: client).setProxyEnabled(
          .with { $0.enabled = enabled }, options: options)
        return ack.operationID
      } catch { throw ClientError.classify(error) }
    }
  }

  /// Forwards App lifetime power events without expressing an ON intent.
  public func notifyPowerEvent(sleeping: Bool, sequence: UInt64) async throws {
    try await withAuthenticatedClient { client in
      var options = CallOptions.defaults
      options.timeout = .seconds(3)
      _ = try await Pax_V1_PaxService.Client(wrapping: client).notifyPowerEvent(
        .with {
          $0.sleeping = sleeping
          $0.sequence = sequence
        }, options: options)
    }
  }

  /// Reports the pages currently visible to the user (whole-set replace). The daemon treats
  /// every page as hidden once no Watch subscription is open.
  public func setViewVisibility(_ views: [String]) async throws {
    try await withAuthenticatedClient { client in
      var options = CallOptions.defaults
      options.timeout = .seconds(3)
      _ = try await Pax_V1_PaxService.Client(wrapping: client).setViewVisibility(
        .with { $0.views = views }, options: options)
    }
  }

  public func saveChainRoute(_ edit: ChainEdit) async throws -> String {
    try await withAuthenticatedClient { client in
      let service = Pax_V1_PaxService.Client(wrapping: client)
      var options = CallOptions.defaults
      options.timeout = .seconds(5)
      let response = try await service.saveChainRoute(
        .with {
          $0.id = edit.id
          $0.name = edit.name
          $0.frontID = edit.front.id
          $0.finalID = edit.final.id
          $0.expectedRevision = edit.revision
          $0.frontRevision = edit.front.revision
          $0.finalRevision = edit.final.revision
          $0.clientID = self.clientID
          $0.requestID = edit.requestID
        }, options: options)
      return response.operationID
    }
  }
  public func routeHistory(_ id: String, metric: String = "", cursor: String = "") async throws
    -> RouteHistoryPage
  {
    try await withAuthenticatedClient { client in
      let service = Pax_V1_PaxService.Client(wrapping: client)
      var options = CallOptions.defaults
      options.timeout = .seconds(5)
      let v = try await service.queryRouteHistory(
        .with {
          $0.routeID = id
          $0.metric = metric
          $0.cursor = cursor
          $0.limit = 100
        }, options: options)
      return RouteHistoryPage(
        items: v.items.map {
          RouteHistorySample(
            metric: $0.metric, network: $0.network, at: $0.at.date,
            configRevision: $0.configRevision,
            value: $0.value, success: $0.success,
            error: $0.hasError ? ProductError(proto: $0.error) : nil,
            exitIP: $0.exitIp, method: $0.method,
            exitLocation: $0.hasExitLocation ? ExitLocation(proto: $0.exitLocation) : nil)
        }, nextCursor: v.nextCursor, partial: v.partial)
    }
  }

  public func reconnectProxy() async throws -> String {
    try await withAuthenticatedClient { client in
      var options = CallOptions.defaults
      options.timeout = .seconds(5)
      do {
        let ack = try await Pax_V1_PaxService.Client(wrapping: client).reconnectProxy(
          .init(), options: options)
        return ack.operationID
      } catch { throw ClientError.classify(error) }
    }
  }

  public func previewRoutes(
    _ source: String, subscription: Bool = false, sourceName: String = ""
  ) async throws -> RoutesPreview {
    do {
      return try await withAuthenticatedClient {
        client in
        var options = CallOptions.defaults
        options.timeout = .seconds(30)
        return RoutesPreview(
          proto: try await Pax_V1_PaxService.Client(wrapping: client).previewRoutes(
            .with {
              $0.source = source
              $0.isSubscription = subscription
              $0.sourceName = sourceName
              $0.clientID = self.clientID
            }, options: options))
      }
    } catch let error as RPCError {
      let known: Set<String> = [
        "subscription_format_unsupported", "invalid_base64", "empty_import", "import_too_large",
        "preview_limit", "catalog_invalid",
        "fetch_forbidden", "fetch_dns_failed", "fetch_tls_failed", "fetch_timeout",
        "fetch_http_failed",
        "fetch_connection_failed",
        "subscription_stale", "subscription_unavailable", "fetch_failed",
        "fetch_scheme_unsupported",
        "fetch_target_disallowed", "fetch_redirect_limit", "fetch_too_large", "fetch_url_invalid",
      ]
      if known.contains(error.message) { throw ProductError(code: error.message) }
      throw ClientError.classify(error)
    } catch { throw ClientError.classify(error) }
  }
  public func cancelPreview(_ token: String) async throws {
    try await withAuthenticatedClient { client in
      _ = try await Pax_V1_PaxService.Client(wrapping: client).cancelRoutesPreview(
        .with {
          $0.token = token
          $0.clientID = self.clientID
        })
    }
  }
  public func confirmPreview(_ preview: RoutesPreview, items: [String], automatic: Bool = false)
    async throws -> String
  {
    try await withAuthenticatedClient { client in
      let ack = try await Pax_V1_PaxService.Client(wrapping: client).confirmImportRoutes(
        .with {
          $0.token = preview.token
          $0.automatic = automatic
          $0.requestID = preview.token
          $0.expectedCatalogRevision = preview.revision
          $0.itemIds = items
          $0.clientID = self.clientID
        })
      return ack.operationID
    }
  }
  public func shutdownAppSession() async throws {
    try await withAuthenticatedClient { client in
      var options = CallOptions.defaults
      options.timeout = .seconds(5)
      _ = try await Pax_V1_PaxService.Client(wrapping: client).shutdownAppSession(
        .init(), options: options)
    }
  }
  public func routeEditor(_ id: String) async throws -> RouteEdit {
    try await withAuthenticatedClient { client in
      RouteEdit(
        proto: try await Pax_V1_PaxService.Client(wrapping: client).getRouteEditor(
          .with { $0.routeID = id }))
    }
  }
  public func confirmTLSBatch(_ routes: [NodeProfile]) async throws -> String {
    try await withAuthenticatedClient { client in
      let ack = try await Pax_V1_PaxService.Client(wrapping: client).confirmRouteTLSBatch(
        .with {
          $0.clientID = self.clientID
          $0.requestID = UUID().uuidString
          $0.routes = routes.map { route in
            .with {
              $0.routeID = route.id
              $0.identityToken = route.tlsPolicy?.identityToken ?? ""
              $0.expectedRevision = route.tlsPolicy?.revision ?? 0
              $0.mode = .skipVerify
              $0.confirmSkip = true
            }
          }
        })
      return ack.operationID
    }
  }
  public func saveRoute(_ edit: RouteEdit) async throws -> String {
    try await withAuthenticatedClient { client in
      let service = Pax_V1_PaxService.Client(wrapping: client)
      var request = edit.proto
      request.clientID = self.clientID
      let ack =
        edit.routeID.isEmpty
        ? try await service.addRoute(request) : try await service.updateRoute(request)
      return ack.operationID
    }
  }
  public func saveRouteGroups(_ edit: RouteGroupsEdit) async throws -> String {
    try await withAuthenticatedClient { client in
      let ack = try await Pax_V1_PaxService.Client(wrapping: client).saveRouteGroups(
        .with {
          $0.groups = edit.groups.map { group in
            .with {
              $0.id = group.id
              $0.name = group.name
              $0.memberIds = group.memberIDs
            }
          }
          $0.expectedCatalogRevision = edit.expectedRevision
          $0.requestID = edit.requestID
          $0.clientID = self.clientID
        })
      return ack.operationID
    }
  }
  public func deleteRoute(_ route: NodeProfile) async throws -> String {
    try await withAuthenticatedClient { client in
      let ack = try await Pax_V1_PaxService.Client(wrapping: client).deleteRoute(
        .with {
          $0.routeID = route.id
          $0.expectedRevision = route.revision
        })
      return ack.operationID
    }
  }
  public func selectRoute(_ route: NodeProfile) async throws -> String {
    try await withAuthenticatedClient { client in
      let ack = try await Pax_V1_PaxService.Client(wrapping: client).selectRoute(
        .with {
          $0.routeID = route.id
          $0.expectedRouteRevision = route.revision
        })
      return ack.operationID
    }
  }

  /// 手动刷新一个订阅来源；返回的 operation id 只代表受理，结果经 Watch 送达。
  public func refreshSubscription(
    _ source: SubscriptionSummary, via route: NodeProfile? = nil, replacementURL: String = "",
    userAgent: String? = nil, useLastChannel: Bool = false
  )
    async throws -> String
  {
    try await withAuthenticatedClient { client in
      var options = CallOptions.defaults
      options.timeout = .seconds(5)
      do {
        let ack = try await Pax_V1_PaxService.Client(wrapping: client).refreshSubscription(
          .with {
            $0.subscriptionID = source.id
            $0.expectedSourceRevision = source.revision
            $0.replacementURL = replacementURL
            if let userAgent { $0.userAgent = userAgent }
            $0.useLastChannel = useLastChannel
            if let route {
              $0.routeID = route.id
              $0.expectedRouteRevision = route.revision
              $0.expectedConfigRevision = route.configRevision
            }
          }, options: options)
        return ack.operationID
      } catch { throw ClientError.classify(error) }
    }
  }

  /// 恢复一条被隐藏的订阅线路；恢复不隐式选择或连接。
  public func deleteSubscription(_ source: SubscriptionSummary, catalogRevision: UInt64)
    async throws -> String
  {
    try await withAuthenticatedClient { client in
      let ack = try await Pax_V1_PaxService.Client(wrapping: client).deleteSubscription(
        .with {
          $0.subscriptionID = source.id
          $0.expectedSourceRevision = source.revision
          $0.expectedCatalogRevision = catalogRevision
          $0.clientID = self.clientID
          $0.requestID = UUID().uuidString
        })
      return ack.operationID
    }
  }
  public func restoreRoute(_ route: NodeProfile) async throws -> String {
    try await withAuthenticatedClient { client in
      let ack = try await Pax_V1_PaxService.Client(wrapping: client).restoreSubscriptionRoute(
        .with {
          $0.routeID = route.id
          $0.expectedRevision = route.revision
        })
      return ack.operationID
    }
  }

  /// 取消仍在进行的后台操作（订阅刷新或线路测试）。
  public func cancelOperation(_ operationID: String) async throws {
    try await withAuthenticatedClient { client in
      _ = try await Pax_V1_PaxService.Client(wrapping: client).cancelOperation(
        .with { $0.operationID = operationID })
    }
  }

  static func makeTransport(socketPath: String) throws -> HTTP2ClientTransport.Posix {
    try .http2NIOPosix(
      target: .unixDomainSocket(path: socketPath),
      transportSecurity: .plaintext
    )
  }

  private static var bundleVersion: String {
    Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "dev"
  }
}

// The deadline observes only establishment, never the lifetime of a healthy stream.
private actor SubscriptionBoundary {
  private(set) var confirmed = false
  func confirm() { confirmed = true }
}

extension DaemonConnection: DaemonTransport {}
