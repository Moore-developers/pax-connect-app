import AppKit

extension PaxAnalysisChart {
  /// Content of one hover card (style H1). Products fill what applies; empty parts are skipped.
  public struct Hover {
    /// Model / effort text with a colour dot.
    public struct Badge {
      public let title: String
      public let color: NSColor
      public init(_ title: String, color: NSColor) {
        self.title = title
        self.color = color
      }
    }
    /// Light-background label, e.g. the band name "超限".
    public struct Tag {
      public let text: String
      public let color: NSColor
      public init(_ text: String, color: NSColor) {
        self.text = text
        self.color = color
      }
    }
    /// Thin progress bar with threshold ticks and a left caption such as "上下文 286k / 272k".
    public struct Gauge {
      public struct Tick {
        public let value: Double
        public let color: NSColor
        public init(_ value: Double, color: NSColor) {
          self.value = value
          self.color = color
        }
      }
      public let value: Double
      public let total: Double
      public let ticks: [Tick]
      public let color: NSColor
      public let label: String
      public init(value: Double, total: Double, ticks: [Tick], color: NSColor, label: String) {
        self.value = value
        self.total = total
        self.ticks = ticks
        self.color = color
        self.label = label
      }
    }
    /// One "swatch name ... amount share" row. `weight` (optional) sizes the stacked bar segment.
    public struct Breakdown {
      public let color: NSColor
      public let name: String
      public let amount: String
      public let share: String
      public let weight: Double
      public init(
        color: NSColor, name: String, amount: String, share: String = "", weight: Double = 0
      ) {
        self.color = color
        self.name = name
        self.amount = amount
        self.share = share
        self.weight = weight
      }
    }
    public let badges: [Badge]
    public let title: String
    public let subtitle: String
    public let summary: [(label: String, value: String)]
    public let proportion: Double?
    public let proportionLabel: String
    public let proportionValue: String
    public let details: [(label: String, value: String)]
    public let context: String
    public let tag: Tag?
    public let gauge: Gauge?
    public let breakdown: [Breakdown]
    public let summarySuffix: String
    public let summaryTagInline: Bool
    public let summaryColor: NSColor?
    public init(
      title: String, subtitle: String, summary: [(label: String, value: String)],
      proportion: Double?, proportionLabel: String, proportionValue: String,
      details: [(label: String, value: String)], context: String, badges: [Badge] = [],
      tag: Tag? = nil, gauge: Gauge? = nil, breakdown: [Breakdown] = [],
      summarySuffix: String = "", summaryTagInline: Bool = false, summaryColor: NSColor? = nil
    ) {
      self.title = title
      self.subtitle = subtitle
      self.summary = summary
      self.proportion = proportion
      self.proportionLabel = proportionLabel
      self.proportionValue = proportionValue
      self.details = details
      self.context = context
      self.badges = badges
      self.tag = tag
      self.gauge = gauge
      self.breakdown = breakdown
      self.summarySuffix = summarySuffix
      self.summaryTagInline = summaryTagInline
      self.summaryColor = summaryColor
    }
  }

  func drawHover(_ index: Int) {
    PaxAnalysisHoverCard.draw(hoverDetails[index], anchorX: x(index), in: bounds)
  }
}
