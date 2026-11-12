package config

import "time"

// CurrentPathRequest freezes the running egress identity, never a caller URL.
type CurrentPathRequest struct {
	CheckID, ExpectedTag, ExpectedSession string
	ExpectedVersion                       uint64
	ConfigRevision                        uint64
}

// CurrentPathResult proves a single current mixed-egress request, not all traffic.
type CurrentPathResult struct {
	CheckID, State, Cause, ActualTag, CoreSession string
	RuntimeVersion                                uint64
	StartedAt, CompletedAt                        time.Time
}
