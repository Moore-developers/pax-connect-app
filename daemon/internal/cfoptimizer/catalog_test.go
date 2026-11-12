package cfoptimizer

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"testing"
	"time"
)

func TestHistorySurvivesFailureNetworkAndReload(t *testing.T) {
	at := time.Date(2026, 10, 15, 14, 3, 0, 0, time.UTC)
	d := NewDocument()
	d.Merge(Record{IP: "104.16.0.1", Network: "A", TCP: Sample{Value: 100, Attempt: at}}, at)
	d.Merge(Record{IP: "104.16.0.1", Network: "A", TCP: Sample{Error: "cf_tcp_unreachable", Attempt: at.Add(time.Minute)}}, at.Add(time.Minute))
	data, err := Encode(d)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	r := loaded.Records[0]
	if r.TCP.Value != 100 || !r.TCP.At.Equal(at) || r.Reachable || r.TCP.Error == "" {
		t.Fatal("failed check erased or greened history", r)
	}
	if r.Network != "A" {
		t.Fatal("another network inherited result")
	}
}
func TestDownloadUsesOneSample(t *testing.T) {
	calls := 0
	var got Record
	p := Probe{Download: func(context.Context, string) (float64, error) { calls++; return 2500, nil }, Now: func() time.Time { return time.Unix(42, 0) }}
	err := Download(context.Background(), "A", []string{"104.16.0.1"}, 1, p, func(r Record) { got = r }, func(Progress) {})
	if err != nil || got.Download.Value != 2500 || got.Download.Rounds != 1 || calls != 1 {
		t.Fatal(got, err, calls)
	}
}
func TestPreciseTCPNeedsTwoSuccessesAndTakesMedian(t *testing.T) {
	calls := 0
	var got Record
	err := MeasureTCP(context.Background(), []string{"104.16.0.1"}, "A", 3, 1, Probe{TCP: func(context.Context, string) (time.Duration, error) {
		calls++
		if calls == 2 {
			return 0, errors.New("fixture")
		}
		return time.Duration(calls) * 100 * time.Millisecond, nil
	}}, func(r Record) { got = r }, func(Progress) {})
	if err != nil || got.TCP.Value != 200 || !got.Reachable {
		t.Fatal(got)
	}
}

func TestBindingFailurePausesWithoutReplacingHistory(t *testing.T) {
	emitted := 0
	err := MeasureTCP(context.Background(), []string{"104.16.0.1", "104.16.0.2"}, "A", 3, 2, Probe{TCP: func(context.Context, string) (time.Duration, error) { return 0, ErrEnvironment }}, func(Record) { emitted++ }, func(Progress) {})
	if !errors.Is(err, ErrEnvironment) || emitted != 0 {
		t.Fatal("binding failure blamed remote IPs", emitted, err)
	}
}

func TestScanUsesBoundedDeepSamplesAndKeepsCoarseOutOfRanking(t *testing.T) {
	var mu sync.Mutex
	records := []Record{}
	phases := []string{}
	p := Probe{TCP: func(_ context.Context, ip string) (time.Duration, error) {
		if ip == "104.16.0.1" {
			return 100 * time.Millisecond, nil
		}
		return 0, errors.New("fixture unreachable")
	}, Now: func() time.Time { return time.Unix(1_760_000_000, 0) }}
	err := Scan(context.Background(), "network", p, rand.New(rand.NewSource(1)), func(r Record) { mu.Lock(); records = append(records, r); mu.Unlock() }, func(v Progress) { phases = append(phases, v.Phase) })
	if err != nil {
		t.Fatal(err)
	}
	if len(DiscoveryTargets()) != 11912 {
		t.Fatal("versioned source range sampling changed", len(DiscoveryTargets()))
	}
	if len(records) > 42 || len(records) < 1 {
		t.Fatal("one seed must deepen at most forty new addresses", len(records))
	}
	if records[0].TCP.Value == 0 {
		t.Fatal("coarse sample leaked into precise ranking")
	}
	precise := false
	for _, r := range records {
		if r.TCP.Value == 100 && r.TCP.Rounds == 3 {
			precise = true
		}
	}
	if !precise || len(phases) == 0 || phases[len(phases)-1] != "precision" {
		t.Fatal("missing independent precision stage")
	}
}

func TestDownloadPublishesWhileAnotherIPIsStillRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	blocked := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	published := make(chan Record, 2)
	p := Probe{Download: func(ctx context.Context, ip string) (float64, error) {
		if ip == "104.16.0.2" {
			close(blocked)
			select {
			case <-release:
				return 2000, nil
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
		<-blocked
		return 1000, nil
	}}
	go func() {
		done <- Download(ctx, "A", []string{"104.16.0.1", "104.16.0.2"}, 2, p, func(r Record) { published <- r }, func(Progress) {})
	}()
	select {
	case first := <-published:
		if first.IP != "104.16.0.1" || first.Download.Value != 1000 || first.Download.Rounds != 1 {
			t.Error(first)
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("completed result waited for blocked peer")
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("download failed to finish")
	}
	second := <-published
	if second.IP != "104.16.0.2" || second.Download.Value != 2000 || second.Download.Rounds != 1 {
		t.Fatal(second)
	}
}

func TestParallelDownloadEnvironmentFailureCancelsAndJoinsPeers(t *testing.T) {
	entered := make(chan struct{})
	exited := make(chan struct{})
	p := Probe{Download: func(ctx context.Context, ip string) (float64, error) {
		if ip == "104.16.0.1" {
			<-entered
			return 0, ErrEnvironment
		}
		close(entered)
		defer close(exited)
		<-ctx.Done()
		return 0, ctx.Err()
	}}
	emitted := 0
	err := Download(context.Background(), "A", []string{"104.16.0.1", "104.16.0.2"}, 2, p, func(Record) { emitted++ }, func(Progress) {})
	if !errors.Is(err, ErrEnvironment) || emitted != 0 {
		t.Fatal("environment became per-IP result", err, emitted)
	}
	select {
	case <-exited:
	default:
		t.Fatal("download returned before cancelling peer")
	}
}

func TestLatestValidationFailureBelongsToAttemptedDraft(t *testing.T) {
	at := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	d := NewDocument()
	d.Merge(Record{IP: "104.16.0.1", Network: "A", Validation: Sample{Value: 100, Attempt: at}, ValidationKey: "draft-A"}, at)
	d.Merge(Record{IP: "104.16.0.1", Network: "A", Validation: Sample{Attempt: at.Add(time.Second), Error: "node_connection_timeout"}, ValidationKey: "draft-B"}, at.Add(time.Second))
	encoded, err := Encode(d)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	got := reloaded.Records[0]
	if got.ValidationKey != "draft-B" || got.Validation.Error != "node_connection_timeout" {
		t.Fatal("latest failure attributed to previous draft", got.ValidationKey, got.Validation.Error)
	}
	if got.Validation.Value != 100 || !got.Validation.At.Equal(at) {
		t.Fatal("historical success erased")
	}
}
