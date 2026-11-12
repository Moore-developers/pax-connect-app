package aiusage

// LimitMoney is an input-only amount in USD; averages retain fractional micro-dollars.
type LimitMoney struct{ Official, Channel float64 }

func (m *LimitMoney) add(v LimitMoney) { m.Official += v.Official; m.Channel += v.Channel }

// ModelLimit is the approved warning/limit preset and its retained official baseline.
type ModelLimit struct {
	Model                            string
	Limit, Warn, FlatLimit, FlatWarn int64
	Baseline                         *float64
}

// LimitBand contains a request count and its priced input amounts.
type LimitBand struct {
	Requests, PricedRequests int64
	Money                    LimitMoney
}

// LimitModelLine identifies the lines contributing to a mixed-model row.
type LimitModelLine struct {
	Model    string
	Lines    *ModelLimit
	Requests int64
}

// LimitFlat estimates input spend without the long-context input multiplier.
type LimitFlat struct {
	Limit, Requests int64
	Cost            LimitMoney
	OverSpend       *LimitMoney
}

// LimitRow is one independently judged session, model/effort, main line or agent.
type LimitRow struct {
	ID, CLI, Session, Title, Model, Effort, Note string
	Emphasized                                   bool
	Bands                                        [3]LimitBand
	Lines                                        *ModelLimit
	ModelLines                                   []LimitModelLine
	Multiple                                     *float64
	OverSpend                                    *LimitMoney
	Flat                                         *LimitFlat
	Unpriced                                     int64
}

// LimitCurrent is the latest retained main request, independent of the date window.
type LimitCurrent struct {
	Context int64
	Limit   *int64
	Band    int32
}

// LimitSummary is the date-window summary; curve follows chronological request order.
type LimitSummary struct {
	Input, OverSpend                                   LimitMoney
	InputTokens                                        int64
	Sessions, OverSessions, Requests, Turns, Subagents int64
	TurnsComplete                                      bool
	Current                                            *LimitCurrent
	Curve                                              []float64
	Bands                                              [3]LimitBand
	Multiple                                           *float64
	Days                                               []LimitDay
	ObservedThrough                                    int64
	Comparison                                         *LimitComparison
}

// LimitDay is one local calendar day's active roots and input cost in the selected range.
type LimitDay struct {
	At, Sessions, Requests, PricedRequests, InputTokens int64
	Input                                               LimitMoney
	Available                                           bool
}

// LimitComparison is the preceding calendar period through the matching observation time.
type LimitComparison struct {
	Input                           LimitMoney
	Sessions, From, To, InputTokens int64
}

// LimitComparisonRange identifies the complete prior calendar period selected by the App.
type LimitComparisonRange struct{ From, To int64 }

// LimitRequest preserves the full per-request input and price breakdown.
type LimitRequest struct {
	Request    AnalysisRow
	Lines      *ModelLimit
	Band       int32
	TurnNumber *int64
}

// LimitSwitch records one model/effort change and official input/cache price ratios.
type LimitSwitch struct {
	Index                           int64
	Kind, From, To                  string
	TurnMultiple, AfterReadMultiple *float64
}

// LimitTrend contains only the selected session's retained main-line requests.
type LimitTrend struct {
	Requests             []LimitRequest
	Lines                *ModelLimit
	OverStart, FlatStart *int64
	Switches             []LimitSwitch
	HasWriteLayer        bool
}

// SessionLimits is the daemon projection consumed by the limits page.
type SessionLimits struct {
	Summary            LimitSummary
	Primary, Secondary []LimitRow
	Trend              *LimitTrend
	AgentsIncomplete   bool
}
