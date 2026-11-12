package core

import (
	"context"
	"errors"
	"google.golang.org/protobuf/types/known/emptypb"
	pb "pax-connect/daemon/gen/proto/corev1"
	"testing"
)

type powerRuntime struct {
	fakeRuntime
	events []bool
}

func (r *powerRuntime) SetPaused(paused bool) error { r.events = append(r.events, paused); return nil }
func TestPowerSequenceRejectsLateSleepAndShutdownDoesNotRevive(t *testing.T) {
	r := &powerRuntime{fakeRuntime: fakeRuntime{state: "STARTED"}}
	s := testServer(t, r)
	for _, req := range []*pb.SetPausedRequest{{Paused: true, Sequence: 1}, {Paused: false, Sequence: 3}, {Paused: true, Sequence: 2}, {Paused: false, Sequence: 3}} {
		if _, err := s.SetPaused(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.events) != 2 || !r.events[0] || r.events[1] {
		t.Fatal(r.events)
	}
	if _, err := s.Shutdown(context.Background(), &emptypb.Empty{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetPaused(context.Background(), &pb.SetPausedRequest{Sequence: 4}); err == nil {
		t.Fatal("power event accepted during shutdown")
	}
	if len(r.events) != 2 {
		t.Fatal("late wake revived service")
	}
}

type retryPowerRuntime struct {
	fakeRuntime
	attempts int
}

func (r *retryPowerRuntime) SetPaused(bool) error {
	r.attempts++
	if r.attempts == 1 {
		return errors.New("fixture unavailable")
	}
	return nil
}
func TestFailedPowerEventCanRetrySameSequence(t *testing.T) {
	r := &retryPowerRuntime{fakeRuntime: fakeRuntime{state: "STARTED"}}
	s := testServer(t, r)
	req := &pb.SetPausedRequest{Sequence: 1}
	if _, err := s.SetPaused(context.Background(), req); err == nil {
		t.Fatal("missing failure")
	}
	if _, err := s.SetPaused(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if r.attempts != 2 {
		t.Fatal("failed sequence consumed")
	}
}
