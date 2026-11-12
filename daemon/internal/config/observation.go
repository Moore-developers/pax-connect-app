package config

import (
	"strings"
	"time"
)

// FlowFact is an immutable observation of a user connection, not an HTTP request.
type FlowFact struct {
	RoutingMatchedAt                                           time.Time `json:",omitempty"`
	Phase                                                      string
	Purpose, IPSource, ProcessStatus                           string
	DNS                                                        *DNSFact `json:",omitempty"`
	ResponseKnown                                              bool
	ResponseMS                                                 uint64
	ObservedUntil                                              *time.Time
	StateUnknown                                               bool
	Key, ApplicationID, Application, Process, Domain, IP       string
	Port                                                       uint16
	Transport, RouteID, RouteName, Direction, Rule, RuleSource string
	StartedAt                                                  time.Time
	ClosedAt                                                   *time.Time
	EstablishmentState                                         string
	EstablishmentMS                                            uint64
	Failure, Evidence                                          string
}

// DNSFact describes one intercepted query, not a connection to an answer IP.
type DNSFact struct {
	QueryType, Result string
	Answers           []string
}

// IsDNSControl requires recorded purpose or an explicit historical DNS action.
// Port 53 alone is insufficient evidence to reclassify old user traffic.
func IsDNSControl(f FlowFact) bool {
	if f.Purpose == "dns" {
		return true
	}
	if e, ok := RecordedRoutingEvidence(f.Rule); ok {
		return e.Method == "builtin" && (e.Target == "DNS 查询" || e.Target == "DNS 接管")
	}
	if strings.HasPrefix(f.RuleSource, "Pax 包：") && (f.Rule == "builtin=DNS 查询" || f.Rule == "builtin=DNS 接管") {
		return true
	}
	return f.Rule == "protocol=dns" || f.Rule == "protocol=dns => hijack-dns"
}

// Observation is the versioned, bounded root-to-user fact envelope.
type Observation struct {
	Session          string
	Sequence         uint64
	At               time.Time
	Kind             string
	Flow             *FlowFact
	Upload, Download uint64
	Raw              string
	Level, Code      string
}

// ObservationBatch carries replayable observations. A missing range is never zero traffic.
type ObservationBatch struct {
	Baseline                  []FlowFact
	ResetToken, NextCursor    string
	BaselineComplete          bool
	BaselineHighWater         uint64
	Session                   string
	HighWater, LostBefore     uint64
	Records                   []Observation
	Collecting, ResetRequired bool
}

// HistoryEvent is a user-private product event. Raw is never included in summaries.
type HistoryEvent struct {
	System                                          *SystemEvent `json:",omitempty"`
	OperationFrom, OperationTo                      string
	Metrics                                         *ConnectionMetrics `json:",omitempty"`
	Sequence                                        uint64
	ID                                              string
	At                                              time.Time
	Kind, Title, Level, Source, Result, OperationID string
	Flow                                            *FlowFact
	Raw                                             []string
	Truncated                                       bool
	RawOmitted                                      string
}

// ObservationRead optionally continues a five-second frozen active baseline.
type ObservationRead struct {
	Session            string
	After              uint64
	Limit              int
	ResetToken, Cursor string
}
