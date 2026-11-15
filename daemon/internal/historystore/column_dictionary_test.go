package historystore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"pax-connect/daemon/internal/config"
)

// Acceptance: the faster typed hash retains the exact digest already stored
// for every field, including zero/max ports and length-delimited separators.
func TestColumnIdentityKeyMatchesStoredEncoding(t *testing.T) {
	for _, port := range []uint16{0, 1, 53, 443, 65535} {
		f := config.FlowFact{Key: "a\x00b", ApplicationID: "应用", Application: "", Process: "/Apps/X Y", Domain: "example.test", IP: "::1", Port: port, Transport: "tcp", RouteID: "r", RouteName: "直连", Direction: "direct", Rule: "method=a\x00b", RuleSource: "source", Purpose: "dns", IPSource: "mapping", ProcessStatus: "known"}
		want := identityDigest(identityValues(f))
		got := identityKey(f)
		if !bytes.Equal(got[:], want) {
			t.Fatalf("stored digest changed for port%d", port)
		}
	}
}

func TestColumnOwnerConnectionDictionaryTracksWritesAndClear(t *testing.T) {
	ctx := context.Background()
	l := mustLogs(t, historyDir(t))
	abort := errors.New("abort")
	write := func(tx *LogsTx, key, name string) error {
		v := config.LogConnectionSummary{Fact: config.FlowFact{Key: key, Application: name, StartedAt: baseHour}, LastLogAt: baseHour}
		return tx.PutSummary(ctx, v)
	}
	if err := l.Update(ctx, func(tx *LogsTx) error {
		if _, ok, err := tx.Summary(ctx, "flow"); err != nil || ok {
			t.Fatal(ok, err)
		}
		if err := write(tx, "flow", "first"); err != nil {
			return err
		}
		got, ok, err := tx.Summary(ctx, "flow")
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
		if got.Fact.Application != "first" {
			t.Fatal(got)
		}
		if err = write(tx, "flow", "next"); err != nil {
			return err
		}
		got, ok, err = tx.Summary(ctx, "flow")
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
		if got.Fact.Application != "next" {
			t.Fatal(got)
		}
		if err = tx.ClearLogTables(ctx); err != nil {
			return err
		}
		if _, ok, err = tx.Summary(ctx, "flow"); err != nil || ok {
			t.Fatal("clear left cached row", ok, err)
		}
		if err = write(tx, "flow", "next"); err != nil {
			return err
		}
		if n := count(t, l.Reader(), "SELECT count(*) FROM col_identities"); n != 0 {
			t.Fatal("reader saw uncommitted identities", n)
		}
		var joined int
		if err = tx.tx.QueryRowContext(ctx, "SELECT count(*) FROM col_connections c JOIN col_identities i ON i.id=c.identity_id").Scan(&joined); err != nil || joined != 1 {
			t.Fatal("clear reused deleted cached identity", joined, err)
		}
		if err = write(tx, "rolled-back", "discard"); err != nil {
			return err
		}
		return abort
	}); !errors.Is(err, abort) {
		t.Fatal(err)
	}
	if err := l.Update(ctx, func(tx *LogsTx) error {
		if len(tx.summaries) != 0 {
			t.Fatal("connection dictionary escaped transaction")
		}
		if _, ok, err := tx.Summary(ctx, "rolled-back"); err != nil || ok {
			t.Fatal(ok, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Acceptance: reuse inside one transaction keeps content-based IDs, but a
// rolled-back ID is never reused by the following transaction. Gap-sized
// rewrites hit a fixed dictionary cap without changing durable identities.
func TestColumnTransactionDictionariesAreBoundedAndRollbackSafe(t *testing.T) {
	ctx := context.Background()
	c := mustColumnsImportTarget(t, historyDir(t))
	if err := c.EnsureProjections(ctx); err != nil {
		t.Fatal(err)
	}
	abort := errors.New("abort")
	if err := c.Update(ctx, func(tx *ColumnsTx) error {
		if _, err := tx.putIdentity(ctx, config.FlowFact{Key: "rolled-back"}); err != nil {
			return err
		}
		if _, err := tx.columnObject(ctx, ColumnMember{Dimension: "domain", Object: "rolled-back"}); err != nil {
			return err
		}
		return abort
	}); !errors.Is(err, abort) {
		t.Fatal(err)
	}
	if err := c.Update(ctx, func(tx *ColumnsTx) error {
		if len(tx.identities) != 0 || len(tx.objects) != 0 {
			t.Fatal("prior transaction dictionaries escaped")
		}
		for i := 0; i < columnBatchDictionaryLimit+8; i++ {
			f := config.FlowFact{Key: fmt.Sprintf("flow-%04d", i)}
			id, err := tx.putIdentity(ctx, f)
			if err != nil {
				return err
			}
			repeated, err := tx.putIdentity(ctx, f)
			if err != nil || id != repeated {
				t.Fatal(id, repeated, err)
			}
			m := ColumnMember{Dimension: "domain", Object: f.Key, Name: f.Key}
			object, err := tx.columnObject(ctx, m)
			if err != nil {
				return err
			}
			repeatedObject, err := tx.columnObject(ctx, m)
			if err != nil || object != repeatedObject {
				t.Fatal(object, repeatedObject, err)
			}
		}
		if len(tx.identities) != columnBatchDictionaryLimit || len(tx.objects) != columnBatchDictionaryLimit {
			t.Fatal("unbounded dictionaries", len(tx.identities), len(tx.objects))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"col_identities", "col_objects"} {
		if n := count(t, c.Reader(), "SELECT count(*) FROM "+table); n != columnBatchDictionaryLimit+8 {
			t.Fatal(table, n)
		}
	}
}
