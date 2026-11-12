package singbox

import (
	"context"
	"fmt"
	"pax-connect/daemon/internal/config"
	"testing"
	"time"
)

func TestLostObservationResetPagesFreezeAllActiveIdentities(t *testing.T) {
	r := NewCoreRuntime(context.Background())
	defer r.Close()
	o := r.observations
	for i := 0; i < 600; i++ {
		key := fmt.Sprintf("flow-%d", i)
		o.flows[key] = &observedFlow{fact: config.FlowFact{Key: key, StartedAt: time.Now()}}
	}
	o.sequence = 900
	o.lostBefore = 500
	q := config.ObservationRead{Session: o.session, After: 1, Limit: 256}
	first := r.ReadObservationPage(context.Background(), q)
	if !first.ResetRequired || len(first.Baseline) != 256 || first.BaselineHighWater != 900 || first.NextCursor == "" {
		t.Fatal("invalid baseline start", first)
	}
	delete(o.flows, "flow-0") // frozen state must not change between pages
	q.ResetToken = first.ResetToken
	q.Cursor = first.NextCursor
	second := r.ReadObservationPage(context.Background(), q)
	q.Cursor = second.NextCursor
	third := r.ReadObservationPage(context.Background(), q)
	if len(second.Baseline) != 256 || len(third.Baseline) != 88 || !third.BaselineComplete || third.BaselineHighWater != 900 {
		t.Fatal("baseline pages lost active identities")
	}
}
