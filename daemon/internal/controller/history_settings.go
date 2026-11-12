package controller

import (
	"context"
	"errors"
	"fmt"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"

	"time"
)

// logOperationExpired applies the shared command validity window before any write.
func logOperationExpired(id string, at time.Time) error {
	if id == "" || len(id) > 128 || time.Since(at) > 24*time.Hour || time.Until(at) > 5*time.Minute {
		return errors.New("operation_expired")
	}
	return nil
}

// ClearLogs deletes events and raw text while keeping flow facts and consumed watermarks.
// The transaction runs inside the log writer, the only goroutine that writes logs.db.
func (c *Controller) ClearLogs(ctx context.Context, q *pb.ClearLogsRequest) (*pb.OperationAck, error) {
	at := q.RequestedAt.AsTime()
	if err := logOperationExpired(q.OperationId, at); err != nil {
		return nil, historyError(err)
	}
	job, err := c.logJob()
	if err != nil {
		return nil, historyError(err)
	}
	result, err := job.submit(ctx, logsCommand{kind: logsCommandClear, id: q.OperationId, signature: "clear", at: at})
	if err != nil {
		return nil, historyError(err)
	}
	c.syncLogSnapshot()
	c.mu.Lock()
	c.current.RecentLogs = nil
	c.mu.Unlock()
	c.completeOperationEvidence(q.OperationId, "日志设置", "", "", "", &config.SystemEvent{Type: "settings.clear_history", Name: "清除日志历史", Category: config.EventCategory{Key: "settings", Name: "设置与数据", Symbol: "gearshape"}, Sections: []config.EventSection{{Kind: "facts", Title: "清除结果", Fields: []config.EventField{{Label: "范围", Value: "日志及原文；不保存被删除内容"}}}}})
	// The icon cache carries its own deletion generation; a failed clear is reported
	// exactly like the legacy implementation. The log data is already deleted here.
	if c.icons != nil {
		if err = c.icons.Clear(result.deletionGeneration); err != nil {
			return nil, historyError(errors.New("icon_cache_unavailable"))
		}
	}
	return &pb.OperationAck{OperationId: q.OperationId}, nil
}

// UpdateSettings changes only the implemented user-private log retention preference.
func (c *Controller) UpdateSettings(ctx context.Context, q *pb.UpdateSettingsRequest) (*pb.OperationAck, error) {
	hours := int(q.GetSettings().GetLogRetentionHours())
	if len(q.FieldMask) != 1 || q.FieldMask[0] != "log_retention_hours" || !validRetentionHours(hours) {
		return nil, historyError(errors.New("invalid_filter"))
	}
	at := q.RequestedAt.AsTime()
	if err := logOperationExpired(q.OperationId, at); err != nil {
		return nil, historyError(err)
	}
	job, err := c.logJob()
	if err != nil {
		return nil, historyError(err)
	}
	signature := querySignature([]any{"retention", hours, q.ExpectedRevision})
	if _, err = job.submit(ctx, logsCommand{kind: logsCommandSettings, id: q.OperationId, signature: signature, at: at, hours: hours, expectedRevision: q.ExpectedRevision}); err != nil {
		return nil, historyError(err)
	}
	c.syncLogSnapshot()
	c.completeOperationEvidence(q.OperationId, "日志设置", "", "", "", &config.SystemEvent{Type: "settings.log_retention", Name: "修改日志保留期", Category: config.EventCategory{Key: "settings", Name: "设置与数据", Symbol: "gearshape"}, Sections: []config.EventSection{{Kind: "facts", Title: "新设置", Fields: []config.EventField{{Label: "保留期", Value: fmt.Sprintf("%d 小时", hours)}}}}})
	return &pb.OperationAck{OperationId: q.OperationId}, nil
}
