package cfoptimizer

import (
	"context"
	"testing"
	"time"
)

func TestTCPPublishesBeforeSlowPeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	blocked := make(chan struct{})
	release := make(chan struct{})
	fastDone := make(chan struct{})
	published := make(chan Record, 2)
	done := make(chan error, 1)
	calls := 0
	probe := Probe{TCP: func(ctx context.Context, ip string) (time.Duration, error) {
		if ip == "104.16.0.2" {
			select {
			case <-blocked:
			default:
				close(blocked)
			}
			select {
			case <-release:
				return 100 * time.Millisecond, nil
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
		<-blocked
		calls++
		if calls == 3 {
			close(fastDone)
		}
		return 90 * time.Millisecond, nil
	}}
	go func() {
		done <- MeasureTCP(ctx, []string{"104.16.0.1", "104.16.0.2"}, "fixture", 3, 2, probe, func(r Record) { published <- r }, func(Progress) {})
	}()
	<-fastDone
	immediate := false
	select {
	case r := <-published:
		immediate = r.IP == "104.16.0.1"
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !immediate {
		t.Fatal("fast IP completed all 3 probes, but no result emitted until slow peer finished")
	}
}

func TestTCPEnvironmentFailureCancelsAndJoinsPeers(t *testing.T) {
	entered := make(chan struct{})
	exited := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	emitted := 0
	err := MeasureTCP(ctx, []string{"104.16.0.1", "104.16.0.2"}, "A", 3, 2, Probe{TCP: func(ctx context.Context, ip string) (time.Duration, error) {
		if ip == "104.16.0.1" {
			<-entered
			return 0, ErrEnvironment
		}
		close(entered)
		defer close(exited)
		<-ctx.Done()
		return 0, ctx.Err()
	}}, func(Record) { emitted++ }, func(Progress) {})
	if err != ErrEnvironment || emitted != 0 {
		t.Fatal("path failure became remote-IP samples", err, emitted)
	}
	select {
	case <-exited:
	default:
		t.Fatal("peer still running after return")
	}
}

func TestTCPCancellationPreservesCompletedFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	done := make(chan error, 1)
	published := make(chan Record, 2)
	go func() {
		done <- MeasureTCP(ctx, []string{"104.16.0.1", "104.16.0.2"}, "A", 3, 2, Probe{TCP: func(ctx context.Context, ip string) (time.Duration, error) {
			if ip == "104.16.0.2" {
				select {
				case <-entered:
				default:
					close(entered)
				}
				<-ctx.Done()
				return 0, ctx.Err()
			}
			<-entered
			return 0, context.DeadlineExceeded
		}}, func(r Record) { published <- r }, func(Progress) {})
	}()
	select {
	case r := <-published:
		if r.IP != "104.16.0.1" || r.TCP.Error != "cf_tcp_unreachable" {
			t.Fatal("missing completed failure", r)
		}
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("completed failure waited for unfinished peer")
	}
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatal(err)
	}
	if len(published) != 0 {
		t.Fatal("cancelled IP published a complete sample")
	}
}
