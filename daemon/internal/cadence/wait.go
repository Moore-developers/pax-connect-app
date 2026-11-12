package cadence

import (
	"context"
	"reflect"
	"time"
)

// After starts a one-shot timer and returns its channel and a stop func. Tests inject a fake.
type After func(d time.Duration) (<-chan time.Time, func())

func realAfter(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTimer(d)
	return t.C, func() { t.Stop() }
}

// Wait sleeps for d or until a wake channel delivers one value. It reports false only when ctx
// ended; true means the timer fired or a wake arrived. d <= 0 returns at once. One call consumes
// at most one wake value, so senders coalesce by using a channel buffered to 1.
func Wait(ctx context.Context, d time.Duration, wakes ...<-chan struct{}) bool {
	return waitWith(ctx, realAfter, d, wakes)
}

func waitWith(ctx context.Context, after After, d time.Duration, wakes []<-chan struct{}) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer, stop := after(d)
	defer stop()
	switch len(wakes) {
	case 0:
		select {
		case <-ctx.Done():
			return false
		case <-timer:
			return true
		}
	case 1:
		select {
		case <-ctx.Done():
			return false
		case <-timer:
			return true
		case <-wakes[0]:
			return true
		}
	}
	cases := make([]reflect.SelectCase, 0, len(wakes)+2)
	cases = append(cases,
		reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())},
		reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(timer)})
	for _, w := range wakes {
		cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(w)})
	}
	i, _, _ := reflect.Select(cases)
	return i != 0
}
