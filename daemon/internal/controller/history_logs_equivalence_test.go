package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

var logsEpoch = time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)

type logsHarness struct {
	t         *testing.T
	base, dir string
	journal   *historystore.Journal
	logs      *historystore.Logs
	l         *historyLogs
	consumers *journalConsumers
	clock     time.Time
}

func newLogsHarness(t *testing.T) *logsHarness {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(parent, "Application Support")
	if err = os.Mkdir(base, 0700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "history")
	ctx := context.Background()
	j, err := historystore.OpenJournal(dir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	logs, _, err := historystore.OpenLogs(ctx, dir, uint32(os.Geteuid()))
	if err != nil {
		_ = j.Close()
		t.Fatal(err)
	}
	h := &logsHarness{t: t, base: base, dir: dir, journal: j, logs: logs, clock: logsEpoch}
	h.consumers = newJournalConsumers()
	if err = h.consumers.Register(HistoryConsumerLogs, historystore.Cursor{}); err != nil {
		t.Fatal(err)
	}
	h.l, err = newHistoryLogs(ctx, logs, j, h.consumers)
	if err != nil {
		t.Fatal(err)
	}
	h.l.now = func() time.Time { return h.clock }
	t.Cleanup(func() {
		_ = j.Close()
		_ = logs.Close()
	})
	return h
}

func (h *logsHarness) appendRows(rows []config.Observation) historystore.Cursor {
	h.t.Helper()
	jrows := make([]historystore.Observation, 0, len(rows))
	var cursor historystore.Cursor
	at := h.clock
	for _, o := range rows {
		jrows = append(jrows, journalRecord(o))
		cursor = historystore.Cursor{Session: o.Session, Seq: o.Sequence}
		at = o.At
	}
	if err := h.journal.Append(context.Background(), at, jrows, cursor); err != nil {
		h.t.Fatal(err)
	}
	return cursor
}

func (h *logsHarness) appendGap(g journalGap, at time.Time, rows []config.Observation) historystore.Cursor {
	h.t.Helper()
	jrows := []historystore.Observation{g.observation(at)}
	var cursor historystore.Cursor
	for _, o := range rows {
		jrows = append(jrows, journalRecord(o))
		cursor = historystore.Cursor{Session: o.Session, Seq: o.Sequence}
	}
	if err := h.journal.Append(context.Background(), at, jrows, cursor); err != nil {
		h.t.Fatal(err)
	}
	return cursor
}

// settle runs one-pass steps until the writer goes idle at the poll interval.
func (h *logsHarness) settle() {
	h.t.Helper()
	for range 100 {
		switch d := h.l.step(context.Background()); d {
		case logsPollInterval:
			return
		case 0:
		default:
			h.t.Fatalf("writer backed off by %v", d)
		}
	}
	h.t.Fatal("writer never went idle")
}

func testLogFlow(key string) *config.FlowFact {
	return &config.FlowFact{Key: key, StartedAt: logsEpoch, Direction: "proxy", RouteID: "route-a",
		RouteName: "Route A", Domain: key + ".example.test", Transport: "tcp", ApplicationID: "app-a", Application: "App A"}
}

func cloneLogFlow(f *config.FlowFact) *config.FlowFact {
	copy := *f
	return &copy
}

func logObservation(seq uint64, kind string, f *config.FlowFact, up, down uint64, raw, level string) config.Observation {
	return config.Observation{Session: "s", Sequence: seq, At: logsEpoch.Add(time.Duration(seq) * time.Second),
		Kind: kind, Flow: f, Upload: up, Download: down, Raw: raw, Level: level}
}

// equivalenceSequence covers every projected stage, a DNS control flow, and one lost range.
func equivalenceSequence() ([]config.Observation, []config.Observation, journalGap) {
	a := testLogFlow("s/1/a")
	b := testLogFlow("s/1/b")
	b.Direction = "direct"
	dns := testLogFlow("s/1/dns")
	dns.Purpose = "dns"
	dns.RouteID = "direct"
	c := testLogFlow("s/1/c")
	e := testLogFlow("s/1/e")
	closed := logsEpoch.Add(7 * time.Second)
	closedC := logsEpoch.Add(15 * time.Second)
	first := []config.Observation{
		logObservation(1, "started", cloneLogFlow(a), 0, 0, "", ""),
		logObservation(2, "sample", cloneLogFlow(a), 10, 100, "", ""),
		logObservation(3, "ready", cloneLogFlow(a), 0, 0, "", ""),
		logObservation(4, "started", cloneLogFlow(b), 0, 0, "", ""),
		logObservation(5, "diagnostic", cloneLogFlow(a), 0, 0, "connection reset by peer", "error"),
		logObservation(6, "rejected", cloneLogFlow(b), 0, 0, "", "warning"),
		logObservation(7, "closed", func() *config.FlowFact { f := cloneLogFlow(a); f.ClosedAt = &closed; return f }(), 0, 0, "", ""),
		logObservation(8, "started", cloneLogFlow(dns), 0, 0, "", ""),
		logObservation(9, "ready", cloneLogFlow(dns), 0, 0, "", ""),
		logObservation(10, "started", cloneLogFlow(c), 0, 0, "", ""),
		logObservation(11, "sample", cloneLogFlow(c), 5, 50, "", ""),
		logObservation(12, "started", cloneLogFlow(e), 0, 0, "", ""),
	}
	failed := cloneLogFlow(e)
	failed.Failure = "connection_timeout"
	second := []config.Observation{
		logObservation(14, "sample", cloneLogFlow(c), 7, 70, "", ""),
		logObservation(15, "closed", func() *config.FlowFact { f := cloneLogFlow(c); f.ClosedAt = &closedC; return f }(), 0, 0, "", ""),
		logObservation(16, "failed", failed, 0, 0, "", "error"),
	}
	return first, second, journalGap{Reason: gapLost, Session: "s", FromSeq: 13, ToSeq: 13, ThroughSeq: 16}
}

func TestLogWritePathMatchesLegacyAcceptObservations(t *testing.T) {
	h := newLogsHarness(t)
	h.l.route = New("test", "test").mapHistoryRoute
	first, second, gap := equivalenceSequence()
	h.appendRows(first) // the journal holds the full sequence the legacy batch carried
	h.appendGap(gap, logsEpoch.Add(13*time.Second), second)
	h.settle()

	oldSummaries, oldEvents, oldDiagnostics := readGoldenLogState(t, "logs")
	newSummaries, newEvents, newDiagnostics := readLogState(t, h.logs)
	if !reflect.DeepEqual(oldSummaries, newSummaries) {
		t.Fatalf("summaries differ:\nold %s\nnew %s", pretty(oldSummaries), pretty(newSummaries))
	}
	if !reflect.DeepEqual(oldEvents, newEvents) {
		t.Fatalf("events differ:\nold %s\nnew %s", pretty(oldEvents), pretty(newEvents))
	}
	if !reflect.DeepEqual(oldDiagnostics, newDiagnostics) {
		t.Fatalf("diagnostics differ:\nold %s\nnew %s", pretty(oldDiagnostics), pretty(newDiagnostics))
	}
	if len(newEvents) < 8 || len(newSummaries) != 5 || len(newDiagnostics) != 1 {
		t.Fatalf("fixture did not exercise the path: %d events, %d summaries, %d diagnostics", len(newEvents), len(newSummaries), len(newDiagnostics))
	}
	open := newSummaries["s/1/e"]
	if open.Metrics.BytesState != "partial" || open.Metrics.Reason != "observation_gap" || !open.Fact.StateUnknown || open.Fact.ObservedUntil == nil {
		t.Fatalf("gap did not mark the open summary: %+v", open)
	}
	closed := newSummaries["s/1/a"]
	if closed.Metrics.BytesState == "partial" && closed.Metrics.Reason == "observation_gap" {
		t.Fatalf("closed summary changed by the gap: %+v", closed)
	}
}

func readLogState(t *testing.T, logs *historystore.Logs) (map[string]config.LogConnectionSummary, []config.HistoryEvent, map[string]string) {
	t.Helper()
	summaries := map[string]config.LogConnectionSummary{}
	diagnostics := map[string]string{}
	rows, err := logs.Reader().Query(`SELECT flow_key,body FROM connections`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key string
		var body []byte
		if err = rows.Scan(&key, &body); err != nil {
			t.Fatal(err)
		}
		var s config.LogConnectionSummary
		if err = json.Unmarshal(body, &s); err != nil {
			t.Fatal(err)
		}
		summaries[key] = s
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	var events []config.HistoryEvent
	// Raw lines are stored in their own column; join them back so the comparison with the
	// legacy event (which kept them in the body) still covers the full event.
	rows, err = logs.Reader().Query(`SELECT body,raw FROM events ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var body, raw []byte
		if err = rows.Scan(&body, &raw); err != nil {
			t.Fatal(err)
		}
		var e config.HistoryEvent
		if err = json.Unmarshal(body, &e); err != nil {
			t.Fatal(err)
		}
		if len(raw) > 0 {
			if err = json.Unmarshal(raw, &e.Raw); err != nil {
				t.Fatal(err)
			}
		}
		events = append(events, e)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	rows, err = logs.Reader().Query(`SELECT flow_key,seq,at_ms,text FROM diagnostics ORDER BY flow_key,seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var flow, text string
		var seq, at int64
		if err = rows.Scan(&flow, &seq, &at, &text); err != nil {
			t.Fatal(err)
		}
		diagnostics[fmt.Sprintf("%s|%020d", flow, seq)] = fmt.Sprintf("%d|%s", at, text)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return summaries, events, diagnostics
}

func pretty(v any) string {
	raw, _ := json.MarshalIndent(v, "", "  ")
	return string(raw)
}
