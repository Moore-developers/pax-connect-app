import Foundation

extension SystemEvent {
  /// Include every retained nested string, with the cache's conservative UTF-8 multiplier.
  var telemetryCacheBytes: Int {
    let header = [type, name, category.key, category.name, category.symbol, result, errorCode]
      .reduce(256) { $0 + $1.utf8.count * 2 }
    return sections.reduce(header) { total, section in
      total + 128 + (section.kind.utf8.count + section.title.utf8.count) * 2
        + section.fields.reduce(0) { fields, field in
          fields + 128
            + [field.label, field.value, field.before, field.after]
            .reduce(0) { $0 + $1.utf8.count * 2 }
        }
    }
  }
}
