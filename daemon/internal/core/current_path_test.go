package core

import (
	"context"
	"google.golang.org/protobuf/types/known/emptypb"
	pb "pax-connect/daemon/gen/proto/corev1"
	"pax-connect/daemon/internal/config"
	"testing"
	"time"
)

type delayedPathRuntime struct {
	fakeRuntime
	entered chan struct{}
	release chan struct{}
}

func (r *delayedPathRuntime) CurrentPathIdentity() (string, uint64) { return "fixture", 1 }
func (r *delayedPathRuntime) PrepareCurrentPath(_ context.Context, q config.CurrentPathRequest) (func(context.Context) config.CurrentPathResult, error) {
	return func(ctx context.Context) config.CurrentPathResult {
		close(r.entered)
		select {
		case <-ctx.Done():
		case <-r.release:
		}
		return config.CurrentPathResult{CheckID: q.CheckID, CoreSession: "fixture", RuntimeVersion: 1, State: "passed"}
	}, nil
}
func TestCurrentPathNetworkDoesNotHoldLifecycleOwner(t *testing.T) {
	r := &delayedPathRuntime{fakeRuntime: fakeRuntime{state: "STARTED"}, entered: make(chan struct{}), release: make(chan struct{})}
	s := testServer(t, r)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = s.VerifyCurrentPath(context.Background(), &pb.CurrentPathRequest{CheckId: "check", ExpectedTag: "tag"})
	}()
	<-r.entered
	cleared := make(chan error, 1)
	go func() { _, err := s.ClearProfile(context.Background(), &emptypb.Empty{}); cleared <- err }()
	select {
	case err := <-cleared:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		close(r.release)
		<-done
		t.Fatal("network holds lifecycle owner")
	}
	close(r.release)
	<-done
}
