package aiusage

import (
	"context"
	"errors"
	"time"
)

// ErrUnavailable is a controlled storage/collection failure without source text.
var ErrUnavailable = errors.New("ai_usage_unavailable")

// ErrRange indicates an unsupported date range or filter.
var ErrRange = errors.New("ai_usage_range_invalid")

// ErrRebuildCode is the state error of a database written by an older schema. The
// daemon never migrates schemas below 5; versions 5/6 accept additive tables only.
const ErrRebuildCode = "ai_usage_rebuild_required"

// Attribution is the Profile owning a request and the multiplier in effect at its time.
type Attribution struct {
	ProfileID, ProfileName string
	ServiceSource          string
	Since                  int64
	Multiplier             float64
	// Models holds per-model multipliers that override Multiplier; nil means none.
	Models map[string]float64
}

// ForModel is the multiplier a request for model pays under this attribution.
func (a Attribution) ForModel(model string) float64 {
	if v, ok := a.Models[model]; ok {
		return v
	}
	return a.Multiplier
}

// Resolver maps a CLI and an instant to the Profile in effect then. It is injected by
// cmd from the profile switch timeline and never sees credentials.
type Resolver func(cli string, at time.Time) Attribution

// Details is a whitelist of derived usage metadata, never source JSON.
type Details struct {
	ServiceSource         string `json:",omitempty"`
	WriteShort, WriteLong *int64
	Prices                [4]*float64
	CacheWritePrices      [2]*float64
	Billed                [4]int64
	PriceVersion          string
	ReasoningEffort       string
	EffortSource          string
}

// Request contains only usage metadata. Times are UTC Unix milliseconds.
type Request struct {
	sourceProvider                                                   string
	sourceStart                                                      int64
	analysis                                                         *analysisEvent
	turn                                                             string
	codex                                                            *codexEvidence
	Details                                                          Details
	Key, CLI, Session, Segment, ProfileID, ProfileName, Model, Error string
	At                                                               int64
	Input, Read, Write, Output                                       int64
	InputPrice, OutputPrice, TPS, Elapsed                            *float64
	Multiplier                                                       float64
	Succeeded, Streaming                                             bool
	Costs                                                            [4]int64
	Cost                                                             int64
}

// Summary is additive; performance sums count only successful known values.
type Summary struct {
	UnconfirmedChannel                         int64
	BilledCosts                                [4]int64
	Count, Success, Input, Read, Write, Output int64
	Costs                                      [4]int64
	Cost                                       int64
	TPSSum, ElapsedSum                         float64
	TPSCount, ElapsedCount                     int64
}

func (s *Summary) add(v Summary) {
	s.UnconfirmedChannel += v.UnconfirmedChannel
	s.Count += v.Count
	s.Success += v.Success
	s.Input += v.Input
	s.Read += v.Read
	s.Write += v.Write
	s.Output += v.Output
	for i := range s.Costs {
		s.Costs[i] += v.Costs[i]
		s.BilledCosts[i] += v.BilledCosts[i]
	}
	s.Cost += v.Cost
	s.TPSSum += v.TPSSum
	s.ElapsedSum += v.ElapsedSum
	s.TPSCount += v.TPSCount
	s.ElapsedCount += v.ElapsedCount
}

// negate flips every additive field, to subtract a stored row from its bucket.
func (s Summary) negate() Summary {
	s.UnconfirmedChannel = -s.UnconfirmedChannel
	s.Count, s.Success, s.Input, s.Read, s.Write, s.Output = -s.Count, -s.Success, -s.Input, -s.Read, -s.Write, -s.Output
	s.Cost, s.TPSSum, s.ElapsedSum, s.TPSCount, s.ElapsedCount = -s.Cost, -s.TPSSum, -s.ElapsedSum, -s.TPSCount, -s.ElapsedCount
	for i := range s.Costs {
		s.Costs[i], s.BilledCosts[i] = -s.Costs[i], -s.BilledCosts[i]
	}
	return s
}

func summarize(r Request) Summary {
	s := Summary{Count: 1, Input: r.Input, Read: r.Read, Write: r.Write, Output: r.Output, Costs: r.Costs, Cost: r.Cost, BilledCosts: r.Details.Billed}
	if r.Details.ServiceSource == "official_account" || r.Details.ServiceSource == "unknown" {
		s.UnconfirmedChannel = 1
	}
	if r.Succeeded {
		s.Success = 1
		if r.TPS != nil {
			s.TPSSum = *r.TPS
			s.TPSCount = 1
		}
		if r.Elapsed != nil {
			s.ElapsedSum = *r.Elapsed
			s.ElapsedCount = 1
		}
	}
	return s
}

// Bucket is an hourly/daily grouping by client, Profile and model.
type Bucket struct {
	ServiceSource                      string
	At                                 int64
	CLI, ProfileID, ProfileName, Model string
	Summary
}

// Query bounds an aggregation or a ten-row log page.
type Query struct {
	ServiceSource                          string
	From, To                               int64
	Granularity, CLI, Profile, Model, Text string
	MinOfficialCost, MaxOfficialCost       *int64
	Result                                 *bool
	Page                                   int
}

// Logs includes result-independent counts and totals after all filters.
type Logs struct {
	Requests             []Request
	All, Success, Failed int64
	Total                Summary
}

// State changes whenever committed usage or collection status changes.
type State struct {
	CatalogRevision uint64
	CatalogReady    bool
	CatalogError    string
	Revision        uint64
	Backfilling     bool
	Skipped         int64
	Error           string
	// Generation advances when stored rows are deleted or the database is rebuilt
	// (protocol.md §4.2 R3). Every advance also advances Revision (R5).
	Generation uint64
}

// Options is fixed at daemon composition. The timeline carries no credentials.
type Options struct {
	// Timeline returns the Profile resolver for one scan; nil attributes nothing.
	Timeline        func(context.Context) (Resolver, error)
	Directory, Home string
	Roots           map[string]string
	Owner           uint32
	Now             func() time.Time
}
