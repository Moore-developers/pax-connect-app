package controller

import (
	"context"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/cadence"
	"pax-connect/daemon/internal/historystore"
	"sync"
	"time"
)

// historyOwner only schedules bounded state publication; storage is owned by the
// independent log/traffic writers and collection by historyIngest.
type historyOwner struct {
	appMu                     sync.Mutex
	applicationInventory      *historyApplicationInventory
	cancel                    context.CancelFunc
	done                      chan struct{}
	lastPublished             time.Time
	publishedHistory          *pb.HistoryState
	publishedSettingsRevision uint64
	publishedDay              string
	publishedLiveCursor       historystore.Cursor
	publishedLiveKnown        bool
	meta                      historyMetadata
	clock                     func() time.Time // read-side clock; nil means time.Now (tests inject)
}

// now is the read-side wall clock for calendar-window queries.
func (c *Controller) now() time.Time {
	if h := c.history; h != nil && h.clock != nil {
		return h.clock()
	}
	return time.Now()
}

// ConfigureHistory initializes history publication without opening or mutating the old library.
func (c *Controller) ConfigureHistory(ctx context.Context, _ string, _ uint32) error {
	lifetime, cancel := context.WithCancel(ctx)
	h := &historyOwner{cancel: cancel, done: make(chan struct{})}
	c.history = h
	// R6: at most one publish per cadence.HistoryPublish. A change signal publishes at once when the
	// last publish is old enough, otherwise it is merged into the publish due at the spacing; the
	// interval round still catches changes nobody signals (live view, calendar day).
	loop := cadence.Loop{
		Name:     "history_publish",
		Interval: func() time.Duration { return cadence.HistoryPublish },
		Wake:     []<-chan struct{}{c.logsChanged},
		Run: func(ctx context.Context) (bool, error) {
			if wait := cadence.HistoryPublish - time.Since(h.lastPublished); wait > 0 && !cadence.Wait(ctx, wait) {
				return false, nil
			}
			c.publishHistory(ctx)
			return true, nil
		},
	}
	go func() {
		defer close(h.done)
		loop.Serve(lifetime)
	}()
	return nil
}

// CloseHistory joins the publication owner after capture and both database jobs stop.
func (c *Controller) CloseHistory(ctx context.Context) error {
	if c.history == nil {
		return nil
	}
	c.history.cancel()
	select {
	case <-c.history.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Controller) drainHistory(ctx context.Context) {
	if t := c.trafficJob(); t != nil {
		_ = t.Flush(ctx)
	}
}
