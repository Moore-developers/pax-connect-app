package singbox

import (
	"context"
	"github.com/sagernet/sing-box/experimental/paxobs"
	"testing"
	"time"
)

// Byte events inside one 5 s window merge into a single sample stamped at the window end (history-storage §4.3).
func TestByteSamplesMergeIntoFiveSecondBuckets(t *testing.T) {
	o := newCoreObservations()
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	o.capture(paxobs.Event{ID: "f", Stage: "accepted", At: base, Inbound: "tun", Domain: "example.test", Port: 443, Network: "tcp"})
	for _, b := range []struct {
		at       time.Duration
		up, down int64
	}{{time.Second, 10, 100}, {3 * time.Second, 5, 50}, {4900 * time.Millisecond, 1, 1}, {5 * time.Second, 7, 70}, {7 * time.Second, 2, 20}} {
		o.capture(paxobs.Event{ID: "f", Stage: "bytes", At: base.Add(b.at), Upload: b.up, Download: b.down})
	}
	runtime := &CoreRuntime{observations: o}
	batch := runtime.ReadObservations(context.Background(), "", 0, 256)
	var samples []struct {
		at       time.Time
		up, down uint64
	}
	for _, r := range batch.Records {
		if r.Kind == "sample" {
			samples = append(samples, struct {
				at       time.Time
				up, down uint64
			}{r.At, r.Upload, r.Download})
		}
	}
	if len(samples) != 2 {
		t.Fatalf("expected two 5 s samples, got %+v", samples)
	}
	if want := base.Add(5 * time.Second); !samples[0].at.Equal(want) || samples[0].up != 16 || samples[0].down != 151 {
		t.Fatalf("first bucket must hold 0-5 s bytes at %v: %+v", want, samples[0])
	}
	if want := base.Add(10 * time.Second); !samples[1].at.Equal(want) || samples[1].up != 9 || samples[1].down != 90 {
		t.Fatalf("second bucket must hold 5-10 s bytes at %v: %+v", want, samples[1])
	}
}

func TestRoutingDecisionTimeIsTerminalAndSamplesKeepIt(t *testing.T) {
	o := newCoreObservations()
	at := time.Now().Add(-time.Minute)
	o.capture(paxobs.Event{ID: "route", Stage: "accepted", At: at, Inbound: "tun"})
	o.capture(paxobs.Event{ID: "route", Stage: "matched", At: at.Add(time.Second), Rule: `{"Package":"优先规则","Method":"domain","Target":"example.test"}`})
	o.capture(paxobs.Event{ID: "route", Stage: "routed", At: at.Add(2 * time.Second), Rule: `{"Package":"优先规则","Method":"domain","Target":"example.test"}`, Outbound: "direct"})
	o.capture(paxobs.Event{ID: "route", Stage: "bytes", At: at.Add(3 * time.Second), Upload: 10})
	o.capture(paxobs.Event{ID: "route", Stage: "closed", At: at.Add(4 * time.Second)})
	o.capture(paxobs.Event{ID: "blocked", Stage: "accepted", At: at, Inbound: "tun"})
	o.capture(paxobs.Event{ID: "blocked", Stage: "matched", At: at.Add(5 * time.Second), Rule: `{"Package":"Pax 内置规则","Method":"builtin","Target":"IPv6 流量"}`})
	o.capture(paxobs.Event{ID: "blocked", Stage: "rejected", At: at.Add(6 * time.Second)})
	runtime := &CoreRuntime{observations: o}
	batch := runtime.ReadObservations(context.Background(), "", 0, 256)
	var route, blocked bool
	for _, v := range batch.Records {
		if v.Flow == nil {
			continue
		}
		want := at.Add(time.Second)
		if v.Flow.Direction == "blocked" {
			blocked = true
			want = at.Add(5 * time.Second)
		} else {
			route = true
		}
		if !v.Flow.RoutingMatchedAt.Equal(want) {
			t.Fatalf("%s decision timestamp changed: %v", v.Kind, v.Flow.RoutingMatchedAt)
		}
	}
	if !route || !blocked {
		t.Fatal("missing terminal decision observations")
	}
}
