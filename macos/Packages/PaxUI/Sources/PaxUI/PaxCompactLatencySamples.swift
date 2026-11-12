/// Display reduction only. The full detail retains its original observations.
enum PaxCompactLatencySamples {
  static func reduce(_ samples: [PaxTrendSample]) -> [PaxTrendSample] {
    let valid = samples.indices.filter { samples[$0].value != nil }
    let limit = PaxTheme.Trend.latencyCompactMaximumSamples
    guard valid.count > limit, let first = valid.first, let last = valid.last else {
      return samples
    }
    // Three consecutive observation groups retain both extremes, plus endpoints.
    let groups = (limit - 2) / 2
    var selected = [first]
    for group in 0..<groups {
      let start = 1 + (valid.count - 2) * group / groups
      let end = 1 + (valid.count - 2) * (group + 1) / groups
      let indices = valid[start..<end]
      guard let low = indices.min(by: { (samples[$0].value ?? 0) < (samples[$1].value ?? 0) }),
        let high = indices.max(by: { (samples[$0].value ?? 0) < (samples[$1].value ?? 0) })
      else { continue }
      selected += low == high ? [low] : [min(low, high), max(low, high)]
    }
    selected.append(last)
    var output: [PaxTrendSample] = []
    var previous: Int?
    for index in selected {
      if let previous,
        let gap = samples[(previous + 1)..<index].first(where: { $0.value == nil })
      {
        output.append(gap)
      }
      let sample = samples[index]
      output.append(
        PaxTrendSample(
          value: sample.value, label: sample.label + " · 列表抽样", position: sample.position))
      previous = index
    }
    return output
  }
}
