package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// logReads is the read-only logs.db surface. It is defined here, in the consumer, so the
// query layer only sees the statements it actually needs.
type logReads interface {
	EachLogEvent(ctx context.Context, fromMs, untilMs, cutoffMs int64, fn func(historystore.LogEvent) error) error
	ReadLogEvent(ctx context.Context, eventID string) (historystore.LogEvent, bool, error)
	LogEventsAfter(ctx context.Context, after, head int64, limit int) ([]historystore.LogEvent, error)
	RecentLogEvents(ctx context.Context, limit int) ([]historystore.LogEvent, error)
	LogConnections(ctx context.Context, keys []string) (map[string]historystore.LogConnection, error)
	EachFlowLogEvent(ctx context.Context, flowKey string, fromMs, untilMs, cutoffMs int64, fn func(historystore.LogEvent) error) error
	EachProblemLogEvent(ctx context.Context, problemKey string, fromMs, untilMs, cutoffMs int64, fn func(historystore.LogEvent) error) error
	LogDiagnostics(ctx context.Context, flowKey string, fromMs, untilMs, head int64) ([]historystore.LogDiagnostic, error)
	SnapshotMeta(ctx context.Context) (historystore.LogsMeta, error)
}

// readTokenCache holds every frozen read token, log and traffic. It lives outside the
// legacy owner goroutine, has its own mutex, and keeps the legacy GLOBAL budget of at most
// eight tokens and 32MiB (history-storage §6); log and traffic kinds share it.
type readTokenCache struct {
	mu                    sync.Mutex
	reads                 map[string]*historyRead
	modelCurrent          *logModelVersion
	logDeletionGeneration uint64
	logRemovalBeforeMs    int64
	logSummaryGeneration  string
	logJournalGeneration  string
	now                   func() time.Time // token age clock; nil means time.Now (tests inject)
}

func (cache *readTokenCache) age(r *historyRead) time.Duration {
	created := r.createdAt
	if created.IsZero() {
		created = r.at
	}
	if cache.now != nil {
		return cache.now().Sub(created)
	}
	return time.Since(created)
}

func newReadTokenCache() *readTokenCache {
	return &readTokenCache{reads: map[string]*historyRead{}}
}

// read returns a live token of kind with the same query signature; an idle or aged token,
// or one with another signature, is dropped.
func (cache *readTokenCache) read(token, kind, signature string) (*historyRead, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	r := cache.reads[token]
	if r == nil || r.logModelRevoked() || r.kind != kind || r.signature != signature || time.Since(r.timeUsed) > 2*time.Minute || cache.age(r) > 5*time.Minute {
		delete(cache.reads, token)
		return nil, errors.New("history_cursor_expired")
	}
	r.timeUsed = time.Now()
	return r, nil
}

// take returns a token of kind without checking a query signature; detail lookups use it.
func (cache *readTokenCache) take(token string, kinds ...string) (*historyRead, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	r := cache.reads[token]
	if r == nil || r.logModelRevoked() || !slices.Contains(kinds, r.kind) || time.Since(r.timeUsed) > 2*time.Minute || cache.age(r) > 5*time.Minute {
		delete(cache.reads, token)
		return nil, errors.New("history_cursor_expired")
	}
	r.timeUsed = time.Now()
	return r, nil
}

// save stores one frozen page under a new token. Log pages carry the generations they were
// read at; a page built while the store was cleared or rebuilt is discarded.
func (cache *readTokenCache) save(r *historyRead) (string, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if r.logModelRevoked() {
		return "", errors.New("history_cursor_expired")
	}
	if r.kind != trafficDetailReadKind && (r.kind == "logs" || r.logEvidence) {
		// Deletion generations only grow. A page read at a newer generation than the cache
		// has seen was read after a commit the cache has not been told about yet: adopt it.
		// An older page was read before a deletion and is discarded.
		if r.deletionGeneration > cache.logDeletionGeneration {
			cache.logDeletionGeneration, cache.logSummaryGeneration = r.deletionGeneration, r.summaryGeneration
			if r.logEvidence {
				cache.logJournalGeneration = r.logJournalGeneration
			}
			cache.dropLogEvidenceLocked()
		}
		safePrefix := r.kind == "logs" && cache.logRemovalBeforeMs > 0 && r.from.UnixMilli() >= cache.logRemovalBeforeMs
		if r.deletionGeneration != cache.logDeletionGeneration && !safePrefix || r.summaryGeneration != cache.logSummaryGeneration || r.logEvidence && r.logJournalGeneration != cache.logJournalGeneration {
			return "", errors.New("history_cursor_expired")
		}
	}
	total := r.bytes
	now := time.Now()
	for key, v := range cache.reads {
		if now.Sub(v.timeUsed) > 2*time.Minute || cache.age(v) > 5*time.Minute {
			delete(cache.reads, key)
		} else {
			total += v.bytes
		}
	}
	for len(cache.reads) >= 8 || total > 32<<20 {
		var oldest string
		var at time.Time
		for k, v := range cache.reads {
			if oldest == "" || v.timeUsed.Before(at) {
				oldest, at = k, v.timeUsed
			}
		}
		if oldest == "" {
			return "", errors.New("history_query_busy")
		}
		total -= cache.reads[oldest].bytes
		delete(cache.reads, oldest)
	}
	if r.logVersion != nil {
		for !cache.modelFitsLocked(r.logVersion) {
			oldest := ""
			var at time.Time
			for key, v := range cache.reads {
				if oldest == "" || v.timeUsed.Before(at) {
					oldest, at = key, v.timeUsed
				}
			}
			if oldest == "" {
				return "", errors.New("history_query_busy")
			}
			delete(cache.reads, oldest)
		}
	}
	token := historyToken()
	if token == "" {
		return "", errors.New("history_unavailable")
	}
	r.timeUsed, r.createdAt = now, now
	cache.reads[token] = r
	return token, nil
}

// observeLogGenerations invalidates log tokens when logs.db reports new deletion or summary
// generations. Traffic tokens containing retained log evidence expire with them.
func (cache *readTokenCache) observeLogGenerations(deletion uint64, summary, journal string, removal ...int64) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	nextJournal := journal
	if deletion == cache.logDeletionGeneration && summary == cache.logSummaryGeneration && nextJournal == cache.logJournalGeneration {
		return
	}
	before := int64(0)
	if len(removal) > 0 {
		before = removal[0]
	}
	scoped := before > 0 && summary == cache.logSummaryGeneration && journal == cache.logJournalGeneration
	cache.logDeletionGeneration, cache.logSummaryGeneration = deletion, summary
	cache.logJournalGeneration = nextJournal
	cache.logRemovalBeforeMs = before
	if !scoped {
		cache.dropLogEvidenceLocked()
		return
	}
	cache.dropLogEvidenceBeforeLocked(before)
}

// invalidate drops every token of one kind, as a committed traffic deletion did before.
func (cache *readTokenCache) invalidate(kind string) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.dropKindLocked(kind)
}

func (cache *readTokenCache) dropKindLocked(kind string) {
	for token, r := range cache.reads {
		if r.kind == kind {
			delete(cache.reads, token)
		}
	}
}

// logJob returns the running log writer, or history_unavailable before configuration.
func (c *Controller) logJob() (*historyLogs, error) {
	c.logsMu.Lock()
	l := c.logs
	c.logsMu.Unlock()
	if l == nil {
		return nil, errors.New("history_unavailable")
	}
	return l, nil
}

// logStore returns the log database through the controller's configured writer.
func (c *Controller) logStore() (logReads, error) {
	c.logsMu.Lock()
	l := c.logs
	c.logsMu.Unlock()
	if l == nil {
		return nil, errors.New("history_unavailable")
	}
	return l.logs, nil
}

// publishedTrafficMeta returns the legacy owner's committed metadata. Log queries take
// every log-related field from logs.db instead (R3) and add these traffic fields.
func (c *Controller) publishedTrafficMeta() historyMetadata {
	if t := c.trafficJob(); t != nil {
		meta := historyMetadata{}
		if stored := t.meta.Load(); stored != nil {
			meta = *stored
		}
		if live := c.liveView.Load(); live != nil {
			if live.Snapshot(time.Now()).Known {
				meta.Collection = "collecting"
			} else {
				meta.Collection = "idle"
			}
		}
		return meta
	}

	c.mu.Lock()
	h := c.current.GetHistory()
	c.mu.Unlock()
	var meta historyMetadata
	if h == nil {
		return meta
	}
	meta.Revision = h.Revision
	meta.Coverage = h.CoverageStart.AsTime()
	meta.Collection = h.CollectionState
	meta.TrafficRevision = h.TrafficRevision
	meta.TrafficDeletionGeneration = h.TrafficDeletionGeneration
	meta.RouteHistoryRevision = h.RouteHistoryRevision
	return meta
}

// logsHistoryMeta composes one log response state from logs.db meta plus the traffic fields
// of the published legacy state. Field mapping matches the old historyState() log section.
func (c *Controller) logsHistoryMeta(m historystore.LogsMeta) historyMetadata {
	meta := c.publishedTrafficMeta()
	meta.RetentionHours = normalizedRetentionHours(m.RetentionHours)
	meta.Retention = int(logRetentionDays(meta.RetentionHours))
	meta.SettingsRevision = m.SettingsRevision
	meta.LogsRevision = m.LogsRevision
	meta.LogDeletionGeneration = m.LogDeletionGeneration
	meta.LogRemovalBeforeMs = m.LogRemovalBeforeMs
	meta.SummaryGeneration = m.SummaryGeneration
	meta.JournalGeneration = m.JournalGeneration
	meta.LogHeadSequence = m.HeadSequence
	meta.ClearGeneration = m.ClearGeneration
	meta.LogClearedAt = m.ClearedAt
	meta.Partial = m.Partial
	meta.CapacityEvicted = m.CapacityEvicted
	meta.Error = m.ErrorCode
	return meta
}

// logQueryEvent decodes one stored row into the product event used by projection. Only
// ReadLogEvent rows carry raw lines; list scans leave Raw empty.
func logQueryEvent(row historystore.LogEvent) (config.HistoryEvent, error) {
	var event config.HistoryEvent
	if err := json.Unmarshal(row.Body, &event); err != nil {
		return event, fmt.Errorf("history logs: decode event %s: %w", row.EventID, err)
	}
	event.Sequence = uint64(row.Sequence)
	if len(row.Raw) > 0 {
		if err := json.Unmarshal(row.Raw, &event.Raw); err != nil {
			return event, fmt.Errorf("history logs: decode event %s raw: %w", row.EventID, err)
		}
	}
	return event, nil
}

// validateLogRead keeps the legacy filter contract of the raw log query.
func validateLogRead(q *pb.LogQuery) error {
	if !validLogView(q.View) || (!validDimension(q.Dimension) && q.Dimension != "system") || len(q.Search) > 200 || q.Level != "" && q.Level != "info" && q.Level != "warning" && q.Level != "error" {
		return errors.New("invalid_filter")
	}
	return validateLogCriteria(q)
}

// logRead freezes one page of events and, for activity, its connection summaries. The
// window, retention, budget, cursor and token rules are the legacy ones.
func (c *Controller) logRead(ctx context.Context, q *pb.LogQuery) (*historyRead, string, error) {
	if err := validateLogRead(q); err != nil {
		return nil, "", err
	}
	sig := querySignature([]any{q.View, q.From, q.Until, q.RouteId})
	if q.ReadToken != "" {
		r, e := c.readTokens.read(q.ReadToken, "logs", sig)
		return r, q.ReadToken, e
	}
	if q.Cursor != "" {
		return nil, "", errors.New("history_cursor_expired")
	}
	source, err := c.logStore()
	if err != nil {
		return nil, "", err
	}
	meta, err := source.SnapshotMeta(ctx)
	if err != nil {
		return nil, "", err
	}
	now := time.Now()
	from := now.Add(-24 * time.Hour)
	until := now
	if q.From != nil {
		from = q.From.AsTime()
	}
	if q.Until != nil {
		until = q.Until.AsTime()
	}
	if !from.Before(until) {
		return nil, "", errors.New("invalid_filter")
	}
	cutoff := now.Add(-time.Duration(meta.RetentionHours) * time.Hour)
	r := &historyRead{at: now, from: from, until: until, kind: "logs", signature: sig,
		meta: c.logsHistoryMeta(meta), deletionGeneration: meta.LogDeletionGeneration, summaryGeneration: meta.SummaryGeneration,
		diagnosticsHead: meta.DiagnosticsHead}
	job, err := c.logJob()
	if err != nil {
		return nil, "", err
	}
	version, err := job.readModel(ctx)
	if err != nil {
		return nil, "", err
	}
	if version != nil {
		meta = version.meta
		r.logVersion = version
		r.meta = c.logsHistoryMeta(meta)
		r.deletionGeneration, r.summaryGeneration = meta.LogDeletionGeneration, meta.SummaryGeneration
		r.logJournalGeneration = meta.JournalGeneration
		r.logEvidence = true
		r.diagnosticsHead = meta.DiagnosticsHead
		cutoff = now.Add(-time.Duration(meta.RetentionHours) * time.Hour)
		for _, entry := range version.candidates[q.View] {
			if err := ctx.Err(); err != nil {
				return nil, "", err
			}
			event := entry.event
			if !event.At.After(from) || event.At.After(until) || event.At.Before(cutoff) {
				continue
			}
			if q.RouteId != "" && (event.Flow == nil || event.Flow.RouteID != q.RouteId) {
				continue
			}
			r.logs = append(r.logs, event)
			r.bytes += 256
		}
		if q.View == "operations" || q.View == "access" {
			for _, entry := range version.candidates["problems"] {
				event := entry.event
				if !event.At.After(from) || event.At.After(until) || event.At.Before(cutoff) {
					continue
				}
				if q.RouteId != "" && (event.Flow == nil || event.Flow.RouteID != q.RouteId) {
					continue
				}
				r.problemLogs = append(r.problemLogs, event)
				r.bytes += 256
			}
		}
	} else {
		err = source.EachLogEvent(ctx, from.UnixMilli(), until.UnixMilli(), cutoff.UnixMilli(), func(row historystore.LogEvent) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			event, err := logQueryEvent(row)
			if err != nil {
				return err
			}
			if !event.At.After(from) || event.At.After(until) || event.At.Before(cutoff) {
				return nil
			}
			if q.RouteId != "" && (event.Flow == nil || event.Flow.RouteID != q.RouteId) {
				return nil
			}
			// Charge the stored body: raw text lives in its own column and is never read here,
			// matching the legacy charge of the raw-free serialization.
			r.bytes += len(row.Body) + 256
			// Separate temporary decode budget; final tokens remain 24MiB and
			// the shared cache remains 32MiB. No unbounded activity staging.
			if r.bytes > 64<<20 || q.View != "activity" && r.bytes > 24<<20 {
				return errors.New("history_query_busy")
			}
			r.logs = append(r.logs, event)
			return nil
		})
	}
	if err != nil {
		return nil, "", err
	}
	sort.Slice(r.logs, func(i, j int) bool {
		if r.logs[i].At.Equal(r.logs[j].At) {
			return r.logs[i].ID > r.logs[j].ID
		}
		return r.logs[i].At.After(r.logs[j].At)
	})
	if q.View == "activity" {
		if err := c.freezeLogRecords(ctx, source, r); err != nil {
			return nil, "", err
		}
		apps, err := c.historyUserApplications(ctx)
		if err != nil {
			return nil, "", err
		}
		if r.logVersion != nil {
			r.userApplications = classifySharedLogSources(r.records, apps)
		} else {
			r.userApplications = classifyLogSources(r.records, apps)
		}
	}
	// A clear or summary rebuild that committed while this page was being read must not
	// be frozen under a token: re-read the generations instead of trusting the first read.
	after, err := source.SnapshotMeta(ctx)
	if err != nil {
		return nil, "", err
	}
	safePrefix := after.LogRemovalBeforeMs > 0 && from.UnixMilli() >= after.LogRemovalBeforeMs
	if after.LogDeletionGeneration != meta.LogDeletionGeneration && !safePrefix || after.SummaryGeneration != meta.SummaryGeneration || after.JournalGeneration != meta.JournalGeneration {
		return nil, "", errors.New("history_cursor_expired")
	}
	if r.bytes > 24<<20 {
		return nil, "", errors.New("history_query_busy")
	}
	token, err := c.readTokens.save(r)
	return r, token, err
}

// QueryLogs reads a frozen page of user-facing events; raw diagnostics are separate.
func (c *Controller) QueryLogs(ctx context.Context, q *pb.LogQuery) (*pb.LogPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := c.logStore(); err != nil {
		return nil, historyError(err)
	}
	var out *pb.LogPage
	var err error
	if q.View != "" {
		out, err = c.queryLogView(ctx, q)
	} else {
		out, err = c.queryLogPage(ctx, q)
	}
	if err != nil {
		return nil, historyError(err)
	}
	if err := c.readTokens.ensureLive(ctx, out.ReadToken); err != nil {
		return nil, historyError(err)
	}
	return out, nil
}

func (c *Controller) queryLogPage(ctx context.Context, q *pb.LogQuery) (*pb.LogPage, error) {
	r, token, e := c.logRead(ctx, q)
	if e != nil {
		return nil, e
	}
	out := &pb.LogPage{ReadToken: token, HeadSequence: r.meta.LogHeadSequence, History: historyState(r.meta)}
	var items []*pb.ProductLog
	for _, v := range r.logs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if q.Level != "" && v.Level != q.Level {
			continue
		}
		id, _ := logObject(v, q.Dimension)
		if q.ObjectId != "" && q.ObjectId != "all" && id != q.ObjectId {
			continue
		}
		items = append(items, eventProto(v))
	}
	start, end, e := historyBoundPage(q.Cursor, len(items), int(q.Limit), 200, querySignature([]string{q.Dimension, q.ObjectId, q.Level}))
	if e != nil {
		return nil, e
	}
	out.Items = items[start:end]
	out.Total = uint64(len(items))
	out.NextCursor = historyBoundNext(end, len(items), querySignature([]string{q.Dimension, q.ObjectId, q.Level}))
	if err := c.readTokens.ensureLive(ctx, out.ReadToken); err != nil {
		return nil, historyError(err)
	}
	return out, nil
}

// QueryLogObjects indexes the entire saved event selection, independently of severity.
func (c *Controller) QueryLogObjects(ctx context.Context, q *pb.LogQuery) (*pb.LogObjectPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := c.logStore(); err != nil {
		return nil, historyError(err)
	}
	var out *pb.LogObjectPage
	var err error
	if q.View != "" {
		out, err = c.queryLogViewObjects(ctx, q)
	} else {
		out, err = c.queryLogObjects(ctx, q)
	}
	if err != nil {
		return nil, historyError(err)
	}
	if err := c.readTokens.ensureLive(ctx, out.ReadToken); err != nil {
		return nil, historyError(err)
	}
	return out, nil
}

func (c *Controller) queryLogObjects(ctx context.Context, q *pb.LogQuery) (*pb.LogObjectPage, error) {
	r, token, e := c.logRead(ctx, q)
	if e != nil {
		return nil, e
	}
	out := &pb.LogObjectPage{ReadToken: token, History: historyState(r.meta), TotalEvents: uint64(len(r.logs))}
	grouped := map[string]*pb.LogObject{}
	for _, v := range r.logs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id, name := logObject(v, q.Dimension)
		if id == "unknown" {
			out.UnknownEvents++
		}
		if grouped[id] == nil {
			grouped[id] = &pb.LogObject{Id: id, Name: name}
		}
		grouped[id].Count++
	}
	var rows []*pb.LogObject
	for _, v := range grouped {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if q.Search == "" || strings.Contains(strings.ToLower(v.Name), strings.ToLower(q.Search)) {
			rows = append(rows, v)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Name == rows[j].Name {
			return rows[i].Id < rows[j].Id
		}
		return rows[i].Name < rows[j].Name
	})
	sig := querySignature([]string{q.Dimension, q.Search})
	start, end, e := historyBoundPage(q.Cursor, len(rows), int(q.Limit), 100, sig)
	if e != nil {
		return nil, e
	}
	out.Items = rows[start:end]
	out.NextCursor = historyBoundNext(end, len(rows), sig)
	return out, nil
}

// Connection charts and pages carry private retained log facts too.
func (cache *readTokenCache) dropLogEvidenceLocked() { cache.dropLogEvidenceBeforeLocked(0) }

func (cache *readTokenCache) dropLogEvidenceBeforeLocked(before int64) {
	for token, r := range cache.reads {
		if r.kind != trafficDetailReadKind && (r.kind == "logs" || r.logEvidence) &&
			(before <= 0 || r.kind != "logs" || r.from.UnixMilli() < before) {
			delete(cache.reads, token)
		}
	}
}
