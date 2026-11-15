package historystore

import (
	"context"
	"testing"

	"pax-connect/daemon/internal/config"
)

// Capacity cleanup keeps ordered DNS answers for live groups while removing
// children of missing groups. Owner keys include an embedded NUL separator.
func TestColumnOrphanCleanupKeepsLiveGroupAnswers(t *testing.T) {
	ctx := context.Background()
	c := snapshotFixtureForOrphan(t)
	if err := c.Update(ctx, func(tx *ColumnsTx) error {
		if _, err := tx.tx.ExecContext(ctx, "INSERT INTO col_answers VALUES(2,?,?,?)", "activity\x00gone", 0, "orphan"); err != nil {
			return err
		}
		return tx.pruneColumnOrphans(ctx, 128)
	}); err != nil {
		t.Fatal(err)
	}
	group, found, err := c.Group(ctx, "activity", "live")
	if err != nil || !found || group.Flow == nil || group.Flow.Fact.DNS == nil {
		t.Fatalf("live group missing: found=%v err=%v", found, err)
	}
	answers := group.Flow.Fact.DNS.Answers
	if len(answers) != 2 || answers[0] != "first" || answers[1] != "second" {
		t.Fatalf("live answers changed: %v", answers)
	}
	var remaining int
	if err = c.r.QueryRowContext(ctx, "SELECT count(*) FROM col_answers WHERE owner_kind=2 AND owner_key=?", "activity\x00gone").Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("orphan answers remaining=%d err=%v", remaining, err)
	}
}

func snapshotFixtureForOrphan(t *testing.T) *Columns {
	t.Helper()
	ctx := context.Background()
	c := mustColumnsImportTarget(t, historyDir(t))
	if err := c.EnsureProjections(ctx); err != nil {
		t.Fatal(err)
	}
	group := ColumnGroup{View: "activity", Key: "live", FirstAt: baseHour, LastAt: baseHour, Count: 1,
		Flow: &ColumnProjectedFlow{Fact: config.FlowFact{DNS: &config.DNSFact{Answers: []string{"first", "second"}}}}}
	if err := c.Update(ctx, func(tx *ColumnsTx) error { return tx.PutGroup(ctx, group) }); err != nil {
		t.Fatal(err)
	}
	return c
}
