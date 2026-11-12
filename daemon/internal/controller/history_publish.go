package controller

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/historystore"
)

// logStateSnapshot returns the last committed logs.db metadata, if the log job runs.
func (c *Controller) logStateSnapshot() (historystore.LogsMeta, bool) {
	meta := c.logState.Load()
	if meta == nil {
		return historystore.LogsMeta{}, false
	}
	return *meta, true
}

// mergeLogHistory overlays the log-owned fields of logs.db onto the published state. The
// log-related partial, capacity_evicted and error_code come only from logs.db (no OR with
// the legacy state); the traffic fields stay on the legacy path (R3); ClearGeneration is a monotonic marker for
// either domain, so the larger value wins and repeated merges stay idempotent.
func mergeLogHistory(base *pb.HistoryState, meta historystore.LogsMeta) *pb.HistoryState {
	if base == nil {
		base = &pb.HistoryState{}
	}
	value := proto.Clone(base).(*pb.HistoryState)
	value.Partial = meta.Partial
	value.CapacityEvicted = meta.CapacityEvicted
	value.ErrorCode = meta.ErrorCode
	value.LogsRevision = meta.LogsRevision
	value.LogDeletionGeneration = meta.LogDeletionGeneration
	value.LogRemovalBeforeMs = meta.LogRemovalBeforeMs
	value.SummaryGeneration = meta.SummaryGeneration
	value.JournalGeneration = meta.JournalGeneration
	value.LogHeadSequence = meta.HeadSequence
	if meta.ClearGeneration > value.ClearGeneration {
		value.ClearGeneration = meta.ClearGeneration
	}
	return value
}

// syncLogSnapshot makes the one visible controller state agree with logs.db before an RPC
// answer: the snapshot already carries the new generations, so icon checks and page
// refreshes cannot read a stale one. It does not fan out a second snapshot; the command's
// operation evidence and the processor notification publish the Watch deltas.
func (c *Controller) syncLogSnapshot() {
	meta, ok := c.logStateSnapshot()
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current.History = mergeLogHistory(c.current.History, meta)
	if c.current.Settings == nil {
		c.current.Settings = &pb.Settings{}
	}
	applyLogSettingsTo(c.current.Settings, meta)
}

// applyLogSettingsTo writes the App-visible retention controls from logs.db meta.
func applyLogSettingsTo(settings *pb.Settings, meta historystore.LogsMeta) {
	hours := normalizedRetentionHours(meta.RetentionHours)
	settings.LogRetentionHours = uint32(hours)
	settings.LogRetentionDays = logRetentionDays(hours)
	settings.Revision = meta.SettingsRevision
}

// recentLogs returns the newest stored events in chronological order for a Watch replace.
// A read failure reports an empty replace, exactly like the legacy best-effort view.
func (c *Controller) recentLogs(ctx context.Context) []*pb.ProductLog {
	source, err := c.logStore()
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	rows, err := source.RecentLogEvents(ctx, 20)
	if err != nil {
		return nil
	}
	out := make([]*pb.ProductLog, 0, len(rows))
	for _, row := range rows {
		event, err := logQueryEvent(row)
		if err != nil {
			continue
		}
		out = append(out, eventProto(event))
	}
	return out
}

// publishHistory publishes traffic and log state together. The log part now comes from the
// committed logs.db metadata and the log database itself; the traffic part is unchanged.
func (c *Controller) publishHistory(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	h := c.history
	h.lastPublished = time.Now()
	logMeta, hasLogs := c.logStateSnapshot()
	trafficMeta := c.publishedTrafficMeta()
	history := historyState(trafficMeta)
	history.RouteHistoryRevision = c.routeHistoryRevision()
	if hasLogs {
		history = mergeLogHistory(history, logMeta)
		history.Partial = history.Partial || trafficMeta.Partial
		history.CapacityEvicted = history.CapacityEvicted || trafficMeta.CapacityEvicted
		if history.ErrorCode == "" {
			history.ErrorCode = trafficMeta.Error
		}
	}
	if !trafficMeta.Coverage.IsZero() {
		history.AggregateCoverageStart = timestamppb.New(trafficMeta.Coverage)
	}
	// R8: status fields travel with their values and never move a revision; a changed value alone
	// is reason enough to push the delta.
	statusChanged := h.publishedHistory != nil && (history.CollectionState != h.publishedHistory.CollectionState || history.Partial != h.publishedHistory.Partial || history.ErrorCode != h.publishedHistory.ErrorCode || history.CapacityEvicted != h.publishedHistory.CapacityEvicted)
	day := time.Now().In(time.Local).Format("2006-01-02 MST -0700")
	logsChanged := h.publishedHistory == nil || h.publishedHistory.LogsRevision != history.LogsRevision || h.publishedHistory.LogDeletionGeneration != history.LogDeletionGeneration
	var liveCursor historystore.Cursor
	var liveKnown bool
	if live := c.liveView.Load(); live != nil {
		live.mu.RLock()
		liveCursor = live.mark
		liveKnown = live.collecting && live.seeded
		live.mu.RUnlock()
	}
	liveChanged := liveCursor != h.publishedLiveCursor || liveKnown != h.publishedLiveKnown
	trafficChanged := liveChanged || h.publishedHistory == nil || h.publishedHistory.TrafficRevision != history.TrafficRevision || h.publishedDay != day || statusChanged
	var recent []*pb.ProductLog
	if logsChanged {
		recent = c.recentLogs(ctx)
	}
	var traffic *pb.TrafficSnapshot
	if trafficChanged {
		traffic, _ = c.trafficToday(ctx)

	}
	trafficFailed := trafficChanged && traffic == nil
	// Sequence/watermark-only commits do not create visible notifications.
	relevantHistory := h.publishedHistory == nil || h.publishedHistory.LogsRevision != history.LogsRevision || h.publishedHistory.TrafficRevision != history.TrafficRevision || statusChanged || h.publishedHistory.RouteHistoryRevision != history.RouteHistoryRevision || h.publishedHistory.LogDeletionGeneration != history.LogDeletionGeneration || h.publishedHistory.TrafficDeletionGeneration != history.TrafficDeletionGeneration || h.publishedHistory.JournalGeneration != history.JournalGeneration || h.publishedHistory.SummaryGeneration != history.SummaryGeneration
	settingsRevision := h.meta.SettingsRevision
	if hasLogs {
		settingsRevision = logMeta.SettingsRevision
	}
	settingsChanged := h.publishedSettingsRevision != settingsRevision
	if !relevantHistory && !trafficChanged && !settingsChanged {
		return
	}
	c.mu.Lock()
	if traffic != nil && proto.Equal(traffic, c.current.TodayTraffic) {
		traffic = nil
	}
	delta := &pb.HistoryDelta{History: history, Traffic: traffic, ReplaceRecentLogs: logsChanged, RecentLogs: recent}
	c.current.History = history
	if logsChanged {
		c.current.RecentLogs = recent
	}
	if traffic != nil {
		c.current.TodayTraffic = traffic
	}
	if c.current.Settings == nil {
		c.current.Settings = &pb.Settings{}
	}
	if hasLogs {
		applyLogSettingsTo(c.current.Settings, logMeta)
	} else {
		h.meta.projectRetention(c.current.Settings)
		c.current.Settings.Revision = h.meta.SettingsRevision
	}
	if settingsChanged {
		c.commitLocked()
	} else {
		c.snapshotSeq++
		c.current.SnapshotVersion = c.snapshotSeq
		c.fanoutLocked(&pb.WatchEvent{Seq: c.snapshotSeq, SnapshotVersion: c.snapshotSeq, Payload: &pb.WatchEvent_HistoryDelta{HistoryDelta: delta}})
	}
	c.mu.Unlock()
	h.publishedHistory = proto.Clone(history).(*pb.HistoryState)
	h.publishedSettingsRevision = settingsRevision
	if trafficFailed {
		h.publishedDay = ""
	} else {
		h.publishedDay = day
	}
	h.publishedLiveCursor, h.publishedLiveKnown = liveCursor, liveKnown
}
