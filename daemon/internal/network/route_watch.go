package network

import (
	"context"
	"sync"
	"time"
)

// Routing-change debounce: a network switch emits dozens of messages in a burst; one
// sample after the burst settles is enough. maxWait stops continuous churn (for example a
// TUN coming up) from postponing the sample forever.
const (
	RouteDebounce = 200 * time.Millisecond
	RouteMaxWait  = time.Second
)

// RouteMessageSource delivers kernel routing-change notifications. Next blocks until a
// message that may change the physical egress arrives; any error means the subscription is
// no longer trustworthy. Close must unblock a pending Next.
type RouteMessageSource interface {
	Next() error
	Close() error
}

// RouteWatchOptions tunes debouncing; zero values use the package defaults.
type RouteWatchOptions struct {
	Debounce, MaxWait time.Duration
}

// RouteWatch is a debounced stream of "the network may have changed" events. When the
// subscription cannot be opened or breaks, Lost is closed and no further events are sent;
// callers then rely on their own polling.
type RouteWatch struct {
	events chan struct{}
	lost   chan struct{}
	done   chan struct{}
	once   sync.Once
	err    error
}

// Events carries at most one pending notification (sends coalesce).
func (w *RouteWatch) Events() <-chan struct{} { return w.events }

// Lost is closed when the subscription is unavailable.
func (w *RouteWatch) Lost() <-chan struct{} { return w.lost }

// Done is closed after every goroutine of the watch has exited.
func (w *RouteWatch) Done() <-chan struct{} { return w.done }

// Err explains why Lost was closed; valid only after Lost is closed.
func (w *RouteWatch) Err() error { return w.err }

func (w *RouteWatch) fail(err error) {
	w.once.Do(func() {
		w.err = err
		close(w.lost)
	})
}

// WatchRoutes opens the source and forwards debounced events until ctx ends.
func WatchRoutes(ctx context.Context, open func() (RouteMessageSource, error), options RouteWatchOptions) *RouteWatch {
	if options.Debounce <= 0 {
		options.Debounce = RouteDebounce
	}
	if options.MaxWait <= 0 {
		options.MaxWait = RouteMaxWait
	}
	w := &RouteWatch{events: make(chan struct{}, 1), lost: make(chan struct{}), done: make(chan struct{})}
	source, err := open()
	if err != nil {
		w.fail(err)
		close(w.done)
		return w
	}
	raw := make(chan struct{}, 1)
	var group sync.WaitGroup
	group.Add(3)
	go func() {
		defer group.Done()
		for {
			if err := source.Next(); err != nil {
				if ctx.Err() == nil {
					w.fail(err)
				}
				return
			}
			select {
			case raw <- struct{}{}:
			default:
			}
		}
	}()
	go func() {
		defer group.Done()
		<-ctx.Done()
		_ = source.Close()
	}()
	go func() {
		defer group.Done()
		w.debounce(ctx, raw, options)
	}()
	go func() {
		group.Wait()
		close(w.done)
	}()
	return w
}

func (w *RouteWatch) debounce(ctx context.Context, raw <-chan struct{}, options RouteWatchOptions) {
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	var first time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.lost:
			return
		case <-raw:
			now := time.Now()
			if first.IsZero() {
				first = now
			}
			wait := options.Debounce
			if remaining := options.MaxWait - now.Sub(first); remaining < wait {
				wait = max(remaining, 0)
			}
			timer.Reset(wait)
		case <-timer.C:
			first = time.Time{}
			select {
			case w.events <- struct{}{}:
			default:
			}
		}
	}
}
