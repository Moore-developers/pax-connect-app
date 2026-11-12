package controller

import (
	"context"
	"errors"
	"os"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"testing"
	"time"
)

func TestFailedMeasurementIsNotAZeroSample(t *testing.T) {
	if got := sampleText(false, "0 B/s"); got != "—（无有效样本）" {
		t.Fatal(got)
	}
	if got := sampleText(false, "0s"); got != "—（无有效样本）" {
		t.Fatal(got)
	}
	if got := sampleText(true, "0 B/s"); got != "0 B/s" {
		t.Fatal("known zero changed", got)
	}
}

func TestFailedRouteMeasurementHistoryDoesNotInventZeroLatency(t *testing.T) {
	c := newTestController(t, &fakeTester{err: errors.New("fixture failed")})
	route := seedLocalRoute(t, c)
	dir := newLogsTestDir(t)
	var e error
	if e = c.ConfigureHistory(context.Background(), dir, uint32(os.Geteuid())); e != nil {
		t.Fatal(e)
	}
	p := attachLogsPipeline(t, c, dir)
	defer func() {
		_ = c.CloseProxy(context.Background())
		_ = c.CloseHistoryLogs(context.Background())
		_ = c.CloseHistory(context.Background())
	}()
	results, stop := operationSink(c)
	defer stop()
	ack, e := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision})
	if e != nil {
		t.Fatal(e)
	}
	awaitResult(t, results, ack.OperationId)
	p.barrier()
	page, e := c.QueryLogs(context.Background(), &paxv1.LogQuery{View: "activity", Scope: "system", Dimension: "system"})
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, r := range page.Records {
		if r.SystemEvent.GetType() == "measurement.route" {
			for _, s := range r.SystemEvent.Sections {
				for _, f := range s.Fields {
					if f.Label == "首次耗时" {
						found = true
						if f.Value != "—（无有效样本）" {
							t.Fatal("failed sample became zero", f.Value)
						}
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("missing actual failed measurement record")
	}
}

type delayedPowerEvidence struct {
	switchingCore
	started chan struct{}
	release chan struct{}
}

func (f *delayedPowerEvidence) Power(context.Context, bool, uint64) error {
	close(f.started)
	<-f.release
	return nil
}
func TestPowerCancelledCallerDoesNotRaceHistoryOwner(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	f := &delayedPowerEvidence{started: make(chan struct{}), release: make(chan struct{})}
	c.ConfigureCoreSession(f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, e := c.NotifyPowerEvent(ctx, &paxv1.PowerEventRequest{Sequence: 10, Sleeping: true})
		done <- e
	}()
	<-f.started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled RPC stayed blocked")
	}
	close(f.release)
	if e := c.query(context.Background(), func(context.Context) {}); e != nil {
		t.Fatal(e)
	}
	if _, e := c.NotifyPowerEvent(context.Background(), &paxv1.PowerEventRequest{Sequence: 10, Sleeping: true}); e != nil {
		t.Fatal(e)
	}
	_ = c.CloseProxy(context.Background())
}
