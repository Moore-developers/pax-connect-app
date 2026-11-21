import Foundation

/// Rejects duplicate keys before JSONDecoder can silently discard their first value.
enum StrictLanguageJSON {
  static func validate(_ data: Data) throws {
    guard String(data: data, encoding: .utf8) != nil else {
      throw LanguagePackFailure("invalid_utf8")
    }
    var parser = Parser(bytes: Array(data))
    try parser.value(depth: 0)
    parser.whitespace()
    guard parser.index == parser.bytes.count else { throw LanguagePackFailure("invalid_json") }
  }
  private struct Parser {
    let bytes: [UInt8]
    var index = 0
    mutating func whitespace() {
      while index < bytes.count, [9, 10, 13, 32].contains(bytes[index]) { index += 1 }
    }
    mutating func consume(_ byte: UInt8) throws {
      whitespace()
      guard index < bytes.count, bytes[index] == byte else {
        throw LanguagePackFailure("invalid_json")
      }
      index += 1
    }
    mutating func string() throws -> String {
      whitespace()
      let start = index
      try consume(34)
      while index < bytes.count {
        let byte = bytes[index]
        index += 1
        if byte == 92 {
          guard index < bytes.count else { throw LanguagePackFailure("invalid_json") }
          index += 1
        } else if byte == 34 {
          do {
            return try JSONDecoder().decode(String.self, from: Data(bytes[start..<index]))
          } catch { throw LanguagePackFailure("invalid_json_string") }
        } else if byte < 32 {
          throw LanguagePackFailure("invalid_json_string")
        }
      }
      throw LanguagePackFailure("invalid_json")
    }
    mutating func value(depth: Int) throws {
      whitespace()
      guard depth <= 12, index < bytes.count else { throw LanguagePackFailure("invalid_json") }
      switch bytes[index] {
      case 123:
        index += 1
        whitespace()
        if index < bytes.count, bytes[index] == 125 {
          index += 1
          return
        }
        var keys = Set<String>()
        while true {
          let key = try string()
          guard keys.insert(key).inserted else {
            throw LanguagePackFailure("duplicate_key", key: key)
          }
          try consume(58)
          try value(depth: depth + 1)
          whitespace()
          if index < bytes.count, bytes[index] == 125 {
            index += 1
            return
          }
          try consume(44)
        }
      case 91:
        index += 1
        whitespace()
        if index < bytes.count, bytes[index] == 93 {
          index += 1
          return
        }
        while true {
          try value(depth: depth + 1)
          whitespace()
          if index < bytes.count, bytes[index] == 93 {
            index += 1
            return
          }
          try consume(44)
        }
      case 34: _ = try string()
      default:
        let start = index
        while index < bytes.count, ![9, 10, 13, 32, 44, 93, 125].contains(bytes[index]) {
          index += 1
        }
        guard index > start else { throw LanguagePackFailure("invalid_json") }
      // JSONDecoder checks primitive syntax and the expected model types afterwards.
      }
    }
  }
}
