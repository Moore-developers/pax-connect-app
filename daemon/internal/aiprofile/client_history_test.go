package aiprofile

import (
	"context"
	"fmt"
	"testing"
)

func TestClientHistoryPagesOnlyRecordedConfigurationChanges(t *testing.T) {
	m, _ := claudeFixture(t)
	ctx := context.Background()
	write := func(key string, cli CLI, kind, name string) {
		t.Helper()
		if err := m.recordEvent(ctx, key, cli, 100, Attribution{ProfileID: key, ProfileName: name}, kind); err != nil {
			t.Fatal(err)
		}
	}
	write("seed:claude", Claude, "apply", "Backfilled")
	write("metadata:one", Claude, "metadata", "Rate changed")
	write("apply:codex", Codex, "apply", "Other client")
	for i := range 50 {
		write(fmt.Sprintf("apply:%d", i), Claude, "apply", fmt.Sprintf("Profile %d", i))
	}
	write("observe:latest", Claude, "observe", "Observed")

	first, next, err := m.ListClientHistory(ctx, Claude, HistoryCursor{})
	if err != nil || len(first) != 50 || next.RowID == 0 || first[0].Kind != "observe" || first[0].ProfileName != "Observed" {
		t.Fatalf("first page: %d events, next %+v, error %v", len(first), next, err)
	}
	// A new write cannot move the boundary of the page already being read.
	write("apply:new", Claude, "apply", "New")
	last, end, err := m.ListClientHistory(ctx, Claude, next)
	if err != nil || len(last) != 1 || end.RowID != 0 || last[0].ProfileName != "Profile 0" {
		t.Fatalf("last page: %+v, next %+v, error %v", last, end, err)
	}
	if _, _, err := m.ListClientHistory(ctx, DSH, HistoryCursor{}); err != Invalid {
		t.Fatalf("usage-only client: %v", err)
	}
}
