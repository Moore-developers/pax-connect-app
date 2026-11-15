package controller

import (
	"context"
	"reflect"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// Acceptance: the bounded fresh-batch shortcut uses the very same product fold
// as a complete SQL reread, including equal-time Sequence versus ID ordering.
// A later append must fall back to the complete retained group.
func TestColumnBatchProjectionMatchesRetainedSQL(t *testing.T) {
	c := openColumnProjectionTest(t)
	ctx := context.Background()
	b := columnProjectionFixture()
	at := b.Events[0].At
	for i := 0; i < 3; i++ {
		b.Events[i].At = at
		b.Events[i].ID = []string{"z", "a", "m"}[i]
	}
	for i := range b.Connections {
		b.Connections[i].LastEventID = "m"
	}
	write := func(batch historystore.ColumnImportBatch) error {
		return c.Update(ctx, func(tx *historystore.ColumnsTx) error {
			for _, s := range batch.Connections {
				if err := tx.PutConnection(ctx, s); err != nil {
					return err
				}
			}
			for _, e := range batch.Events {
				if _, err := tx.PutEvent(ctx, e); err != nil {
					return err
				}
			}
			return projectColumnBatch(ctx, tx, batch)
		})
	}
	if err := write(b); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		for _, view := range []string{"activity", "access", "problems", "operations"} {
			keys, err := c.GroupKeys(ctx, view, "", 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range keys {
				got, ok, err := c.Group(ctx, view, key)
				if err != nil || !ok {
					t.Fatal(ok, err)
				}
				want, err := projectColumnGroup(ctx, c, view, key)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("batch differs %s/%s", view, key)
				}
			}
		}
	}
	check()
	e := b.Events[0]
	e.Sequence = 100
	e.ID = "later"
	e.At = e.At.Add(time.Second)
	if err := write(historystore.ColumnImportBatch{Events: []config.HistoryEvent{e}}); err != nil {
		t.Fatal(err)
	}
	check()
}
