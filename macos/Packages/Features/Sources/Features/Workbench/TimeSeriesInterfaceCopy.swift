import PaxCore
import PaxUI

func stackedChartSummary(_ context: PresentationContext)
  -> @Sendable (String, String, String) -> String
{
  { unit, secondary, samples in
    context.text(
      "chart.stacked_summary", ["unit": unit, "secondary": secondary, "samples": samples])
  }
}

func timeSeriesCopy(_ context: PresentationContext) -> PaxTimeSeriesCopy {
  var copy = PaxTimeSeriesCopy()
  copy.missing = context.text("chart.missing")
  copy.partial = context.text("chart.partial")
  copy.total = context.text("chart.total")
  copy.range = context.text("chart.range")
  copy.noData = context.text("chart.no_data")
  copy.latest = context.text("chart.latest")
  copy.maximum = context.text("chart.maximum")
  copy.samples = { context.text("chart.samples", ["count": String($0)]) }
  copy.gaps = { context.text("chart.gaps", ["count": String($0)]) }
  copy.coverage = { context.text("chart.coverage", ["time": $0]) }
  return copy
}
