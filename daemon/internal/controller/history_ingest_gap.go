package controller

import (
	"crypto/rand"
	"encoding/json"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
	"time"
)

// Gap markers (history-storage HS-R5): the journal holds a synthetic observation of kind
// journalGapKind wherever data is known to be missing, so the log and traffic jobs can
// project the gap instead of inventing zeros.
const (
	journalGapKind    = "gap"
	journalGapSession = "gap:" // synthetic sessions never collide with a core session's (session, seq)
	// journalPollKind marks one successful core read cycle (ticket 85). The traffic job derives
	// collection coverage from consecutive polls; the log job ignores them.
	journalPollKind    = "poll"
	journalPollSession = "poll:"

	gapSessionChanged = "session_change"  // the core restarted: its old tail and new head are unseen
	gapLost           = "lost"            // the core buffer overflowed past our cursor
	gapReset          = "reset_required"  // the core demanded a baseline for another reason
	gapEvicted        = "journal_evicted" // a journal file was removed by the capacity cap before it was consumed
	gapDropped        = "journal_dropped" // a journal file was corrupt or had another schema and was quarantined
	gapCursorAhead    = "cursor_ahead"    // our cursor is beyond the core's high water
)

// journalGap is the JSON body of a gap observation.
//
// Core gaps (session_change, lost, reset_required, cursor_ahead) fill Session (the core session after the gap)
// and FromSeq..ToSeq, the undelivered records in that session; PrevSession is set when the session changed.
// Journal gaps (journal_evicted, journal_dropped) fill FromMs..ToMs, the wall-clock range of the lost hour
// file, which holds records appended in that hour.
type journalGap struct {
	Reason      string `json:"reason"`
	Session     string `json:"session,omitempty"`
	PrevSession string `json:"prev_session,omitempty"`
	FromSeq     uint64 `json:"from_seq,omitempty"`
	ToSeq       uint64 `json:"to_seq,omitempty"`
	FromMs      int64  `json:"from_ms,omitempty"`
	ToMs        int64  `json:"to_ms,omitempty"`
	// ThroughSeq is the highest sequence committed in the same journal transaction after
	// this gap. The legacy writer treated that whole core page as gapped, so the log job
	// reproduces the same per-row gap flag without needing transaction boundaries.
	ThroughSeq uint64 `json:"through_seq,omitempty"`
}

// journalPoll is the JSON body of a poll observation, the coverage evidence of the traffic job.
// Session is the core session of the reads, Collecting whether the core was collecting, AtMs the daemon
// wall clock of the last read it covers.
//
// With FromMs set, one row proves collection over the whole [FromMs, AtMs): the writer saw every read in
// that span succeed within trafficPollSpacing, with the same session and Collecting. Empty reads write
// nothing; their span is carried in memory and written with the next page, with a gap, when the span
// ends, or at most ingestCoverageFlush later (so a crash loses at most that much coverage).
// Without FromMs (rows written before this form) two consecutive polls of one session, both collecting, at
// most trafficPollSpacing apart and with no gap marker between them, prove the seconds in between (HS-R5).
type journalPoll struct {
	Session    string `json:"session"`
	Collecting bool   `json:"collecting"`
	AtMs       int64  `json:"at_ms"`
	FromMs     int64  `json:"from_ms,omitempty"`
}

// observation encodes the poll as a journal row with a unique synthetic (session, seq).
func (p journalPoll) observation() historystore.Observation {
	body, _ := json.Marshal(p) // string, bool and integer only: Marshal cannot fail
	return historystore.Observation{Session: journalPollSession + rand.Text(), Seq: 1, AtMs: p.AtMs, Kind: journalPollKind, Body: body}
}

// detectCoreGap reports a discontinuity between the committed cursor and what the core now offers.
// seeding means we asked for the baseline ourselves, so the core's ResetRequired alone proves nothing.
func detectCoreGap(cursor historystore.Cursor, b config.ObservationBatch, seeding bool) *journalGap {
	gap := journalGap{Session: b.Session, FromSeq: cursor.Seq + 1}
	high := b.HighWater
	if b.ResetToken != "" {
		high = b.BaselineHighWater
	}
	switch {
	case cursor.Session != "" && cursor.Session != b.Session:
		gap.Reason, gap.PrevSession, gap.FromSeq, gap.ToSeq = gapSessionChanged, cursor.Session, 1, high
	case cursor.Session == b.Session && cursor.Seq > b.HighWater:
		gap.Reason, gap.FromSeq, gap.ToSeq = gapCursorAhead, 1, high
	case b.LostBefore > cursor.Seq+1:
		gap.Reason, gap.ToSeq = gapLost, b.LostBefore-1
	case b.ResetRequired && !seeding:
		gap.Reason, gap.ToSeq = gapReset, max(high, cursor.Seq)
	default:
		return nil
	}
	return &gap
}

// evictionGap describes a whole hour file lost to the capacity cap or to corruption.
func evictionGap(reason string, hour time.Time) journalGap {
	return journalGap{Reason: reason, FromMs: hour.UnixMilli(), ToMs: hour.Add(time.Hour).UnixMilli()}
}

// observation encodes the gap as a journal row with a unique synthetic (session, seq).
func (g journalGap) observation(at time.Time) historystore.Observation {
	body, _ := json.Marshal(g) // strings and integers only: Marshal cannot fail
	return historystore.Observation{Session: journalGapSession + rand.Text(), Seq: 1, AtMs: at.UnixMilli(), Kind: journalGapKind, Body: body}
}

// journalRecord encodes a core observation as a journal row; Body is the observation JSON.
func journalRecord(o config.Observation) historystore.Observation {
	body, _ := json.Marshal(o) // strings, numbers, times and slices of them: Marshal cannot fail
	row := historystore.Observation{Session: o.Session, Seq: o.Sequence, AtMs: o.At.UnixMilli(), Kind: o.Kind, Body: body}
	if o.Flow != nil {
		row.FlowKey = o.Flow.Key
	}
	return row
}
