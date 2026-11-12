import Foundation

/// Checks ambiguity before Foundation normalizes JSON; daemon remains the authoritative validator.
struct RouteDocumentSyntax {
  private let bytes: [UInt8]
  private var index = 0
  init(_ text: String) { bytes = Array(text.utf8) }
  mutating func validate() throws {
    guard bytes.count <= 65_536 else { throw invalid }
    whitespace()
    guard index < bytes.count, bytes[index] == 123 else { throw invalid }
    try value(depth: 0)
    whitespace()
    guard index == bytes.count else { throw invalid }
  }
  private var invalid: RouteWorkbenchStore.EditError {
    RouteWorkbenchStore.EditError("节点 JSON 无效：请检查重复字段、括号、深度或 64 KiB 大小限制")
  }
  private mutating func whitespace() {
    while index < bytes.count && [9, 10, 13, 32].contains(bytes[index]) { index += 1 }
  }
  private mutating func consume(_ byte: UInt8) throws {
    whitespace()
    guard index < bytes.count, bytes[index] == byte else { throw invalid }
    index += 1
  }
  private mutating func string() throws -> String {
    whitespace()
    let start = index
    try consume(34)
    while index < bytes.count {
      let byte = bytes[index]
      index += 1
      if byte == 92 {
        guard index < bytes.count else { throw invalid }
        index += 1
      } else if byte == 34 {
        guard
          let result = try JSONSerialization.jsonObject(
            with: Data(bytes[start..<index]), options: .fragmentsAllowed) as? String
        else { throw invalid }
        return result
      }
    }
    throw invalid
  }
  private mutating func value(depth: Int) throws {
    guard depth <= 32 else { throw invalid }
    whitespace()
    guard index < bytes.count else { throw invalid }
    switch bytes[index] {
    case 123:
      index += 1
      whitespace()
      if index < bytes.count && bytes[index] == 125 {
        index += 1
        return
      }
      var keys = Set<String>()
      while true {
        let key = try string()
        guard keys.insert(key).inserted else { throw invalid }
        try consume(58)
        try value(depth: depth + 1)
        whitespace()
        guard index < bytes.count else { throw invalid }
        if bytes[index] == 125 {
          index += 1
          return
        }
        try consume(44)
      }
    case 91:
      index += 1
      whitespace()
      if index < bytes.count && bytes[index] == 93 {
        index += 1
        return
      }
      while true {
        try value(depth: depth + 1)
        whitespace()
        guard index < bytes.count else { throw invalid }
        if bytes[index] == 93 {
          index += 1
          return
        }
        try consume(44)
      }
    case 34: _ = try string()
    default:
      let start = index
      while index < bytes.count && ![9, 10, 13, 32, 44, 93, 125].contains(bytes[index]) {
        index += 1
      }
      guard start < index else { throw invalid }
      _ = try JSONSerialization.jsonObject(
        with: Data(bytes[start..<index]), options: .fragmentsAllowed)
    }
  }
}
