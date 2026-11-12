package historystore

import (
	"context"
	"testing"
	"time"
)

func TestJournalHourOfLocatesCursorFile(t *testing.T) {
	ctx := context.Background()
	j := mustJournal(t, historyDir(t))
	for i, seq := range []uint64{1, 2} {
		at := baseHour.Add(time.Duration(i) * time.Hour)
		if err := j.Append(ctx, at, []Observation{obs("s", seq)}, Cursor{"s", seq}); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name   string
		cursor Cursor
		want   time.Time
		ok     bool
	}{
		{"zero cursor", Cursor{}, time.Time{}, false},
		{"unknown session", Cursor{"other", 5}, time.Time{}, false},
		{"first file", Cursor{"s", 1}, baseHour, true},
		{"second file", Cursor{"s", 2}, baseHour.Add(time.Hour), true},
		{"beyond newest row", Cursor{"s", 9}, baseHour.Add(time.Hour), true},
	}
	for _, c := range cases {
		got, ok, err := j.HourOf(ctx, c.cursor)
		if err != nil || ok != c.ok || !got.Equal(c.want) {
			t.Fatalf("%s: got %v %v %v, want %v %v", c.name, got, ok, err, c.want, c.ok)
		}
	}
}
