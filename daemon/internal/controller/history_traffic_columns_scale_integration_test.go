//go:build integration

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// TestColumnTrafficBuildNearCapacity builds the legacy ~512MiB diagnostic fixture,
// not the current 2GiB capacity acceptance dataset. It adds evidence, never padding.
// The source stays untouched. Stable sampling keeps each selected connection's
// entire retained event chain, while independent events use their own identity.
func TestColumnTrafficBuildNearCapacity(t *testing.T) {
	source, target := os.Getenv("PAX_COLUMN_SCALE_SOURCE"), os.Getenv("PAX_COLUMN_SCALE_TARGET")
	if source == "" || target == "" {
		t.Skip("explicit converted source and NEW /private/tmp target required")
	}
	if !strings.HasPrefix(target, "/private/tmp/") {
		t.Fatal("target must be new private temporary directory")
	}
	if info, err := os.Stat(filepath.Join(source, "logs.db-wal")); err == nil && info.Size() > 0 {
		t.Fatal("source must be closed/checkpointed before copying")
	}
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	start := time.Now()
	input, err := os.Open(filepath.Join(source, "logs.db"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(filepath.Join(target, "logs.db"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(out, input); err != nil {
		t.Fatal(err)
	}
	if err = input.Close(); err != nil {
		t.Fatal(err)
	}
	if err = out.Close(); err != nil {
		t.Fatal(err)
	}
	src, err := historystore.OpenColumns(ctx, source, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	dst, _, err := historystore.OpenLogs(ctx, target, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dst.Close() }()
	dst.ConfigureColumnProjector(projectColumnBatch)
	for {
		done, e := advanceColumnTrafficIndex(ctx, dst.Columns())
		if e != nil {
			t.Fatal(e)
		}
		if done {
			break
		}
	}
	meta, err := dst.SnapshotMeta(ctx)
	if err != nil {
		t.Fatal(err)
	}
	baseSeq := meta.HeadSequence
	before, err := dst.LiveBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	threshold := int(10000 * (float64(493<<20)/float64(before) - 1))
	if threshold < 1 || threshold > 9000 {
		t.Fatalf("unexpected source size for near-cap: %d", before)
	}
	selected := func(key string) bool {
		sum := sha256.Sum256([]byte(key))
		return binary.BigEndian.Uint64(sum[:8])%10000 < uint64(threshold)
	}
	prefix := "scaled/"
	events, connections, expectedMissingLast := 0, 0, 0
	var after uint64
	for {
		page, err := src.EventsPage(ctx, after, 128)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		after = page[len(page)-1].Sequence
		var batch []config.HistoryEvent
		for _, e := range page {
			key := e.ID
			if e.Flow != nil && e.Flow.Key != "" {
				key = e.Flow.Key
			}
			if !selected(key) {
				continue
			}
			e.ID = prefix + e.ID
			e.Sequence = baseSeq + e.Sequence
			if e.Flow != nil && e.Flow.Key != "" {
				f := *e.Flow
				f.Key = prefix + f.Key
				e.Flow = &f
			}
			batch = append(batch, e)
		}
		if err = dst.Update(ctx, func(tx *historystore.LogsTx) error {
			for _, e := range batch {
				inserted, seq, err := tx.AppendEvent(ctx, e)
				if err != nil {
					return err
				}
				if !inserted || uint64(seq) != e.Sequence {
					return fmt.Errorf("scaled event sequence was not retained: %s", e.ID)
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		events += len(batch)
	}
	afterKey := ""
	for {
		page, err := src.ConnectionsPage(ctx, afterKey, 128)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		afterKey = page[len(page)-1].Fact.Key
		if err = dst.Update(ctx, func(tx *historystore.LogsTx) error {
			for _, s := range page {
				if !selected(s.Fact.Key) {
					continue
				}
				var activity int64
				var hasLast bool
				if err := src.Reader().QueryRowContext(ctx, "SELECT last_activity_ms,EXISTS(SELECT 1 FROM col_events e WHERE e.event_id=c.last_event_id) FROM col_connections c WHERE flow_key=?", s.Fact.Key).Scan(&activity, &hasLast); err != nil {
					return err
				}
				if s.LastEventID != "" && !hasLast {
					expectedMissingLast++
				}
				s.Fact.Key = prefix + s.Fact.Key
				if s.LastEventID != "" {
					s.LastEventID = prefix + s.LastEventID
				}
				if s.LastSequence > 0 {
					s.LastSequence += baseSeq
				}
				// A clone must retain its original retention timestamp while
				// exercising the same typed writer as ordinary production.
				at := s.Metrics.AsOf
				if s.LastLogAt.After(at) {
					at = s.LastLogAt
				}
				if at.IsZero() {
					at = s.Fact.StartedAt
				}
				if activity != at.UnixMilli() {
					return fmt.Errorf("source retention timestamp differs from standard summary: %s", s.Fact.Key)
				}
				if err = tx.PutSummary(ctx, s); err != nil {
					return err
				}
				connections++
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = dst.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	bytes, err := dst.LiveBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var missing int
	if err = dst.Columns().Reader().QueryRowContext(ctx, "SELECT count(*) FROM col_connections c WHERE c.flow_key LIKE 'scaled/%' AND c.last_event_id<>'' AND NOT EXISTS(SELECT 1 FROM col_events e WHERE e.event_id=c.last_event_id)").Scan(&missing); err != nil {
		t.Fatal(err)
	}
	if missing != expectedMissingLast {
		t.Fatalf("cloning lost retained last-event references: got=%d inherited_missing=%d", missing, expectedMissingLast)
	}
	t.Logf("COLUMN_NEAR_CAP sample_permyriad=%d before_bytes=%d final_bytes=%d final_mib=%.3f cloned_events=%d cloned_connections=%d elapsed_ms=%.3f preserved_times=true traffic_db_scale=1", threshold, before, bytes, float64(bytes)/(1<<20), events, connections, float64(time.Since(start).Microseconds())/1000)
	if bytes < 480<<20 || bytes > 505<<20 {
		t.Fatalf("near-cap target not reached: %.3fMiB", float64(bytes)/(1<<20))
	}
}
