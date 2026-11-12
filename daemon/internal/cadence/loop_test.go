package cadence

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeAfter hands out timer channels the test fires by hand.
type fakeAfter struct {
	asked chan time.Duration
	fire  chan chan time.Time
}

func newFake() *fakeAfter {
	return &fakeAfter{asked: make(chan time.Duration, 16), fire: make(chan chan time.Time, 16)}
}

func (f *fakeAfter) after(d time.Duration) (<-chan time.Time, func()) {
	c := make(chan time.Time, 1)
	f.asked <- d
	f.fire <- c
	return c, func() {}
}

// next waits for the loop to start a wait and returns its duration; fire releases that wait.
// State changed between next and fire is visible to the following round (channel ordering).
func (f *fakeAfter) next() time.Duration { return <-f.asked }
func (f *fakeAfter) fireNext()           { (<-f.fire) <- time.Time{} }

func TestWaitContextWakeAndTimer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	wake := make(chan struct{}, 1)
	wake2 := make(chan struct{}, 1)
	wake2 <- struct{}{}
	if !Wait(ctx, time.Hour, wake, wake2) {
		t.Fatal("wake on second channel should return true")
	}
	wake <- struct{}{}
	if !Wait(ctx, time.Hour, wake) {
		t.Fatal("single wake should return true")
	}
	if !Wait(ctx, time.Millisecond) {
		t.Fatal("timer should return true")
	}
	if !Wait(ctx, 0) {
		t.Fatal("d<=0 with live ctx should return true")
	}
	cancel()
	if Wait(ctx, time.Hour, wake, wake2) || Wait(ctx, 0) || Wait(ctx, time.Hour) {
		t.Fatal("cancelled ctx should return false")
	}
}

func TestLoopGateChangeErrorAndInterval(t *testing.T) {
	f := newFake()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var runs, changes, skips, errs int
	gated := false
	interval := time.Second
	l := Loop{
		Name:     "t",
		RunFirst: true,
		After:    f.after,
		Interval: func() time.Duration { return interval },
		Gate: func() string {
			if gated {
				return "off"
			}
			return ""
		},
		Run: func(context.Context) (bool, error) {
			runs++
			if runs == 2 {
				return false, errors.New("boom")
			}
			return runs == 1, nil
		},
		OnChange: func() { changes++ },
		OnSkip:   func(string, string) { skips++ },
		OnError:  func(string, error) { errs++ },
	}
	go func() { l.Serve(ctx); close(done) }()
	if d := f.next(); d != time.Second { // round 1 (changed) done
		t.Fatalf("interval = %v", d)
	}
	interval = 2 * time.Second
	f.fireNext() // round 2 errors
	if d := f.next(); d != 2*time.Second {
		t.Fatalf("interval = %v", d)
	}
	gated = true
	f.fireNext() // round 3 skipped
	f.next()
	gated = false
	f.fireNext() // round 4 runs
	f.next()     // round 4 complete
	cancel()
	<-done
	if runs != 3 || changes != 1 || skips != 1 || errs != 1 {
		t.Fatalf("runs=%d changes=%d skips=%d errs=%d", runs, changes, skips, errs)
	}
}

func TestLoopWakeRunsEarlyWithoutOverlap(t *testing.T) {
	wake := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	running, overlap := 0, false
	ran := make(chan struct{}, 4)
	l := Loop{
		Name:     "w",
		Interval: func() time.Duration { return time.Hour },
		Wake:     []<-chan struct{}{wake},
		Run: func(context.Context) (bool, error) {
			running++
			overlap = overlap || running > 1
			running--
			ran <- struct{}{}
			return false, nil
		},
	}
	done := make(chan struct{})
	go func() { l.Serve(ctx); close(done) }()
	wake <- struct{}{}
	<-ran
	wake <- struct{}{}
	<-ran
	cancel()
	<-done
	if overlap {
		t.Fatal("rounds overlapped")
	}
}

func TestAIUsageInterval(t *testing.T) {
	if AIUsage(true) != AIUsageVisible || AIUsage(false) != AIUsageHidden {
		t.Fatal("AIUsage mapping")
	}
}
