package controller

import (
	"context"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/telemetry"
)

// RecordEvent commits one explicit product event. It never synthesizes connection success.
func (c *Controller) RecordEvent(ctx context.Context, e config.HistoryEvent) error {
	var err error
	e, err = cloneSystemEvidence(e)
	if err != nil {
		return err
	}
	for i, raw := range e.Raw {
		safe, truncated, omitted := telemetry.ScrubRaw(raw)
		e.Raw[i] = safe
		e.Truncated = e.Truncated || truncated
		if omitted != "" {
			e.RawOmitted = omitted
		}
	}
	job, err := c.logJob()
	if err != nil {
		return err
	}
	if err = validLogEvent(e); err != nil {
		return err
	}
	// Same bounded inbox as enqueueHistoryEvent; the flush keeps RecordEvent's contract that
	// the event is committed to logs.db when the call returns.
	job.enqueue(e)
	if err = job.Flush(ctx); err != nil {
		return err
	}
	return nil
}
func (c *Controller) enqueueHistoryEvent(e config.HistoryEvent) {
	var err error
	e, err = cloneSystemEvidence(e)
	if err != nil {
		return
	}
	c.logsMu.Lock()
	l := c.logs
	c.logsMu.Unlock()
	if l != nil {
		l.enqueue(e)
	}
}
