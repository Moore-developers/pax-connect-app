import AppKit

/// Shared time-series chart for the traffic latency list/detail and route usage (SCT).
/// Hover, click pin, arrow keys and Escape live here once; drawing is in the +Draw file.
@MainActor
public final class PaxTimeSeriesChart: NSView {
  public enum Density { case compact, regular }

  public let density: Density
  public var style: PaxTimeSeriesStyle { didSet { rebuild() } }
  public var series: PaxTimeSeries {
    didSet {
      guard series != oldValue else { return }
      rebuild()
    }
  }
  /// Fade the area toward the baseline while retaining the shared curve and interaction.
  public var fadesAreaFill = false { didSet { needsDisplay = true } }
  public var tooltipCoordinator: PaxChartTooltipCoordinator?
  /// Identifies this chart across rebuilds so a pinned bucket can be restored after a refresh.
  public var pinKey: String? { didSet { if oldValue != pinKey { interaction.escape() } } }
  public var onSelect: ((PaxTimeSeriesSelection?) -> Void)?

  private(set) var model: PaxTimeSeriesLayout
  var interaction = PaxTimeSeriesInteraction()
  var plot = NSRect.zero
  private var plotLeft: CGFloat = 0
  private var selectionDamage = NSRect.zero
  private var mouseTrackingArea: NSTrackingArea?
  private var suspended = false
  private var hadFocus = false
  private var pendingRestore: PaxTimeSeriesPinMemory.Restored?

  public init(series: PaxTimeSeries, style: PaxTimeSeriesStyle, density: Density) {
    self.series = series
    self.style = style
    self.density = density
    model = Self.makeModel(series: series, style: style, density: density)
    super.init(frame: .zero)
    wantsLayer = true
    layerContentsRedrawPolicy = .onSetNeedsDisplay
    setAccessibilityElement(true)
    setAccessibilityRole(.image)
    setAccessibilityLabel(model.summary(style: style))
    updateInset()
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }

  public override var isFlipped: Bool { true }
  public override func setFrameSize(_ newSize: NSSize) {
    let changed = frame.size != newSize
    super.setFrameSize(newSize)
    if changed { needsDisplay = true }
  }

  public override var acceptsFirstResponder: Bool { true }

  /// True when at least one observed bucket exists; hosts hide the chart otherwise.
  public var hasData: Bool { !model.knownIndices.isEmpty }
  var selection: PaxTimeSeriesSelection? {
    currentHit.map { model.selection(for: $0, style: style) }
  }
  var currentHit: PaxTimeSeriesLayout.Hit? {
    interaction.selected.flatMap { model.resolve($0, sampleID: interaction.sampleID) }
  }

  private static func makeModel(
    series: PaxTimeSeries, style: PaxTimeSeriesStyle, density: Density
  ) -> PaxTimeSeriesLayout {
    let source =
      density == .compact && series.axis == .time ? PaxTimeSeriesCompaction.reduce(series) : series
    return PaxTimeSeriesLayout(
      points: source.points, domain: source.domain, minimumCeiling: style.minimumCeiling,
      axis: source.axis)
  }

  private func rebuild() {
    model = Self.makeModel(series: series, style: style, density: density)
    updateInset()
    let wasPinned = interaction.pinned
    interaction.refresh(in: model)
    if wasPinned { commitMemory() }
    setAccessibilityLabel(model.summary(style: style))
    refreshPresentation()
  }

  private func updateInset() {
    guard density == .regular else {
      plotLeft = PaxTheme.Trend.inset
      return
    }
    let attrs: [NSAttributedString.Key: Any] = [.font: PaxTheme.Typography.caption()]
    let widest =
      model.gridValues.map { (style.formatTick($0) as NSString).size(withAttributes: attrs).width }
      .max() ?? 0
    plotLeft = max(PaxTheme.Routes.chartLeft, ceil(widest) + PaxTheme.Spacing.tight)
  }

  func updatePlot() {
    switch density {
    case .compact:
      plot = bounds.insetBy(dx: PaxTheme.Trend.inset, dy: PaxTheme.Trend.inset)
    case .regular:
      plot = NSRect(
        x: plotLeft, y: PaxTheme.Routes.chartTop,
        width: max(1, bounds.width - plotLeft - PaxTheme.Spacing.tight),
        height: max(1, bounds.height - PaxTheme.Routes.chartAxis - PaxTheme.Routes.chartTop))
    }
  }

  // MARK: Presentation

  func fraction(of hit: PaxTimeSeriesLayout.Hit) -> Double {
    if let index = hit.index { return model.fraction(at: index) }
    let half = (model.domain.bucket ?? 0) / 2
    return model.fraction(of: hit.date.addingTimeInterval(half))
  }

  func anchor(for hit: PaxTimeSeriesLayout.Hit) -> NSRect {
    updatePlot()
    let x = plot.minX + CGFloat(fraction(of: hit)) * plot.width
    var y = plot.maxY
    if let index = hit.index, let total = model.points[index].total {
      y = plot.maxY - CGFloat(model.yFraction(total)) * plot.height
    }
    return NSRect(x: x, y: y, width: 1, height: 1)
  }

  /// Where the shared readout attaches: the data point, shifted so a readout of `width`
  /// stays inside this chart's visible bounds (the rightmost bucket would overhang the sheet).
  func tooltipAnchor(for hit: PaxTimeSeriesLayout.Hit, readoutWidth: CGFloat = 0) -> NSRect {
    var rect = anchor(for: hit)
    let visible = bounds.intersection(visibleRect.isEmpty ? bounds : visibleRect)
    let half = min(readoutWidth, visible.width) / 2
    let x = max(visible.minX + half, min(visible.maxX - half, rect.midX))
    rect.origin.x = x - rect.width / 2
    rect.origin.y = min(max(rect.origin.y, visible.minY), visible.maxY - rect.height)
    return rect
  }

  private func refreshPresentation(redraw: Bool = true, selectionOnly: Bool = false) {
    if redraw {
      if selectionOnly { invalidateSelection() } else { needsDisplay = true }
    }
    guard let hit = currentHit else {
      if !suspended { tooltipCoordinator?.close(from: self) }
      setAccessibilityValue("")
      if redraw { onSelect?(nil) }
      return
    }
    let chosen = model.selection(for: hit, style: style)
    setAccessibilityValue(chosen.note)
    let text = tooltipText(for: hit)
    tooltipCoordinator?.show(
      text, from: self,
      anchor: tooltipAnchor(for: hit, readoutWidth: PaxTimeSeriesText.readoutWidth(text))
    ) {
      [weak self] in self?.dismissed()
    }
    if redraw { onSelect?(chosen) }
  }

  /// Sample charts show timestamp and milliseconds at either density; compact time charts
  /// keep their existing value-only readout.
  func tooltipText(for hit: PaxTimeSeriesLayout.Hit) -> String {
    density == .compact && series.axis == .time
      ? model.compactReadout(for: hit, style: style) : model.selection(for: hit, style: style).note
  }

  /// Right edge of what compact drawing paints (plot edge plus the endpoint dot), in this
  /// view's coordinates. Hosts use it to place the chart relative to neighbouring text.
  var drawnRightEdge: CGFloat {
    updatePlot()
    return plot.maxX + lastMarkRadius
  }

  /// How far the compact drawing stays inside the frame on the right: the plot inset minus the
  /// radius of the last mark (endpoint dot, or the larger isolated dot).
  var drawnTrailingInset: CGFloat { PaxTheme.Trend.inset - lastMarkRadius }

  private var lastMarkRadius: CGFloat {
    if case .dot = model.segments.last { return PaxTheme.TimeSeries.isolatedDotRadius }
    return PaxTheme.ChartPlot.endpointRadius
  }

  /// The shared tooltip was closed by someone else (another chart, Escape, a re-render).
  private func dismissed() {
    guard !suspended else { return }
    interaction.escape()
    invalidateSelection()
    if let key = pinKey { PaxTimeSeriesPinMemory.forget(key: key) }
    setAccessibilityValue("")
    onSelect?(nil)
  }

  private func commitMemory() {
    guard let key = pinKey else { return }
    if interaction.pinned, let date = interaction.selected {
      PaxTimeSeriesPinMemory.remember(date, sampleID: interaction.sampleID, key: key)
    } else {
      PaxTimeSeriesPinMemory.forget(key: key)
    }
  }

  private func commit(previous: PaxTimeSeriesInteraction? = nil) {
    let changed = previous != interaction
    if changed { commitMemory() }
    // Tooltip geometry still follows scrolling even if the selected sample is unchanged.
    refreshPresentation(redraw: changed, selectionOnly: true)
  }

  private func invalidateSelection() {
    let previous = selectionDamage
    updateSelectionDamage()
    if !previous.isEmpty { setNeedsDisplay(previous) }
    if !selectionDamage.isEmpty { setNeedsDisplay(selectionDamage) }
  }

  func updateSelectionDamage() {
    updatePlot()
    if let hit = currentHit {
      let x = plot.minX + CGFloat(fraction(of: hit)) * plot.width
      let padding = PaxTheme.ChartPlot.selectionRadius + PaxTheme.ChartPlot.selectionHalo + 1
      selectionDamage = NSRect(
        x: x - padding, y: plot.minY - padding,
        width: padding * 2, height: plot.height + padding * 2)
    } else {
      selectionDamage = .zero
    }
  }

  // MARK: Events

  private func hit(for event: NSEvent) -> PaxTimeSeriesLayout.Hit? {
    updatePlot()
    let location = convert(event.locationInWindow, from: nil)
    guard location.x >= plot.minX, location.x <= plot.maxX, plot.width > 0 else { return nil }
    return model.hit(atFraction: Double((location.x - plot.minX) / plot.width))
  }

  public override func updateTrackingAreas() {
    super.updateTrackingAreas()
    // AppKit maintains this area's visible rect during scrolling and resizing.
    // Keep its identity instead of unnecessarily resetting mouse tracking.
    if let mouseTrackingArea, trackingAreas.contains(where: { $0 === mouseTrackingArea }) {
      return
    }
    let area = NSTrackingArea(
      rect: .zero,
      options: [.mouseMoved, .mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect],
      owner: self)
    mouseTrackingArea = area
    addTrackingArea(area)
  }

  public override func mouseEntered(with event: NSEvent) { mouseMoved(with: event) }

  public override func mouseMoved(with event: NSEvent) {
    guard tooltipCoordinator?.suppressesHover(in: self) != true else { return }
    let previous = interaction
    guard isTopmost(at: event) else {
      interaction.leave()
      commit(previous: previous)
      return
    }
    guard !interaction.pinned else {
      if tooltipCoordinator?.isShowing(from: self) == false {
        refreshPresentation(redraw: false)
      }
      return
    }
    interaction.hover(hit(for: event))
    commit(previous: previous)
  }

  /// Tracking areas fire even when an in-window overlay covers the chart, so hover, tooltip
  /// and pin are only honoured when this chart (or a descendant) is the view hit-tested there.
  private func isTopmost(at event: NSEvent) -> Bool {
    guard let root = window?.contentView else { return true }
    let point =
      root.superview?.convert(event.locationInWindow, from: nil)
      ?? event.locationInWindow
    guard let top = root.hitTest(point) else { return false }
    return top === self || top.isDescendant(of: self)
  }

  public override func mouseDown(with event: NSEvent) {
    guard isTopmost(at: event) else { return }
    window?.makeFirstResponder(self)
    interaction.click(hit(for: event))
    commit()
  }

  public override func mouseExited(with event: NSEvent) {
    let previous = interaction
    interaction.leave()
    commit(previous: previous)
  }

  public override func keyDown(with event: NSEvent) {
    switch event.keyCode {
    case 123: interaction.move(model.neighbor(of: currentHit, step: -1))
    case 124: interaction.move(model.neighbor(of: currentHit, step: 1))
    case 53:
      guard clearSelection() else {
        super.keyDown(with: event)
        return
      }
      return
    default:
      super.keyDown(with: event)
      return
    }
    commit()
  }

  /// Escape clears the pin / selection first. False when there was nothing to clear, so the
  /// caller (detail sheet) may treat Escape as its own close.
  @discardableResult
  func clearSelection() -> Bool {
    guard interaction.selected != nil else { return false }
    interaction.escape()
    commit()
    return true
  }

  /// Sheets bind Escape as a key equivalent that runs before `keyDown`; claim it while this
  /// chart is the first responder and has something to clear.
  public override func performKeyEquivalent(with event: NSEvent) -> Bool {
    guard event.keyCode == 53, window?.firstResponder === self, clearSelection() else {
      return super.performKeyEquivalent(with: event)
    }
    return true
  }

  /// Escape handling for hosts that intercept keys before the responder chain (route overlay).
  public static func consumeEscape(in responder: NSResponder?) -> Bool {
    (responder as? PaxTimeSeriesChart)?.clearSelection() ?? false
  }

  // MARK: Lifecycle

  public override func viewDidMoveToWindow() {
    super.viewDidMoveToWindow()
    guard window != nil, let key = pinKey else { return }
    pendingRestore = PaxTimeSeriesPinMemory.take(key: key)
    needsLayout = true
  }

  public override func layout() {
    super.layout()
    guard let restore = pendingRestore, let window, bounds.width > 0 else { return }
    pendingRestore = nil
    if restore.focused { window.makeFirstResponder(self) }
    guard let date = restore.date, model.resolve(date, sampleID: restore.sampleID) != nil else {
      return
    }
    interaction.pin(at: date, sampleID: restore.sampleID)
    commit()
  }

  public override func viewWillMove(toWindow newWindow: NSWindow?) {
    if newWindow == nil {
      // A rebuild removes this view; keep the pinned bucket so the replacement can restore it.
      suspended = true
      if let key = pinKey {
        let date = interaction.pinned ? interaction.selected : nil
        PaxTimeSeriesPinMemory.release(
          date, sampleID: interaction.sampleID,
          focused: window?.firstResponder === self || hadFocus, key: key)
      }
      tooltipCoordinator?.close(from: self)
      suspended = false
    }
    super.viewWillMove(toWindow: newWindow)
  }

  public override func becomeFirstResponder() -> Bool {
    hadFocus = true
    return super.becomeFirstResponder()
  }

  public override func resignFirstResponder() -> Bool {
    // Removal resigns before `viewWillMove`; only a live window means the user moved on.
    if superview != nil, window != nil { hadFocus = false }
    return super.resignFirstResponder()
  }

  public override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }
}
