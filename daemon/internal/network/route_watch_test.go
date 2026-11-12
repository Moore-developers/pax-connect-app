package network

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type fakeRouteSource struct {
	messages chan error
	once     sync.Once
}

func newFakeRouteSource() *fakeRouteSource { return &fakeRouteSource{messages: make(chan error, 64)} }
func (f *fakeRouteSource) Next() error {
	err, ok := <-f.messages
	if !ok {
		return errors.New("closed")
	}
	return err
}
func (f *fakeRouteSource) Close() error { f.once.Do(func() { close(f.messages) }); return nil }

func countEvents(w *RouteWatch) (count *atomic.Int32) {
	count = new(atomic.Int32)
	go func() {
		for {
			select {
			case <-w.Events():
				count.Add(1)
			case <-w.Done():
				return
			}
		}
	}()
	return count
}

func TestRouteWatchCoalescesBurstIntoOneEventAfterDebounce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		source := newFakeRouteSource()
		ctx, cancel := context.WithCancel(context.Background())
		w := WatchRoutes(ctx, func() (RouteMessageSource, error) { return source, nil }, RouteWatchOptions{})
		count := countEvents(w)
		for range 10 {
			source.messages <- nil
			time.Sleep(10 * time.Millisecond)
		}
		synctest.Wait()
		if count.Load() != 0 {
			t.Fatal("event fired before the burst settled", count.Load())
		}
		time.Sleep(RouteDebounce + time.Millisecond)
		synctest.Wait()
		if count.Load() != 1 {
			t.Fatal("burst was not coalesced into one event", count.Load())
		}
		source.messages <- nil
		time.Sleep(RouteDebounce + time.Millisecond)
		synctest.Wait()
		if count.Load() != 2 {
			t.Fatal("later change did not produce its own event", count.Load())
		}
		cancel()
		<-w.Done()
	})
}

func TestRouteWatchMaxWaitBoundsContinuousChurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		source := newFakeRouteSource()
		ctx, cancel := context.WithCancel(context.Background())
		w := WatchRoutes(ctx, func() (RouteMessageSource, error) { return source, nil }, RouteWatchOptions{})
		count := countEvents(w)
		// A message every 100ms never leaves a quiet 200ms gap.
		for range 25 {
			source.messages <- nil
			time.Sleep(100 * time.Millisecond)
		}
		synctest.Wait()
		if count.Load() < 2 {
			t.Fatal("continuous churn starved the event", count.Load())
		}
		cancel()
		<-w.Done()
	})
}

func TestRouteWatchReportsOpenAndReadFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		boom := errors.New("socket denied")
		w := WatchRoutes(context.Background(), func() (RouteMessageSource, error) { return nil, boom }, RouteWatchOptions{})
		<-w.Lost()
		<-w.Done()
		if !errors.Is(w.Err(), boom) {
			t.Fatal(w.Err())
		}

		source := newFakeRouteSource()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		w = WatchRoutes(ctx, func() (RouteMessageSource, error) { return source, nil }, RouteWatchOptions{})
		source.messages <- errors.New("read failed")
		<-w.Lost()
		cancel()
		<-w.Done()
		if w.Err() == nil {
			t.Fatal("read failure lost its cause")
		}
	})
}

func TestRouteWatchCancelStopsEveryGoroutine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		source := newFakeRouteSource()
		ctx, cancel := context.WithCancel(context.Background())
		w := WatchRoutes(ctx, func() (RouteMessageSource, error) { return source, nil }, RouteWatchOptions{})
		cancel()
		<-w.Done()
		select {
		case <-w.Lost():
			t.Fatal("cancellation is not a subscription failure")
		default:
		}
	})
}
