package aiprofile

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Event is one row of the profile switch timeline. At is Unix nanoseconds.
// Kind "apply" switches a CLI to a Profile; "metadata" records a Profile's multiplier change.
type Event struct {
	Key  string
	CLI  CLI
	At   int64
	Kind string
	Attribution
}

// Timeline answers which Profile owned a CLI at one instant, and at which multiplier.
// It holds no credentials and is a snapshot: load a new one to see later events.
type Timeline struct {
	apply  map[CLI][]Event
	prices map[string][]Event
}

func newTimeline(events []Event) *Timeline {
	t := &Timeline{apply: map[CLI][]Event{}, prices: map[string][]Event{}}
	for _, e := range events {
		switch e.Kind {
		case "apply", "observe":
			t.apply[e.CLI] = append(t.apply[e.CLI], e)
		case "metadata":
			t.prices[e.ProfileID] = append(t.prices[e.ProfileID], e)
		}
	}
	for _, list := range t.apply {
		sort.SliceStable(list, func(i, j int) bool { return list[i].At < list[j].At })
	}
	for _, list := range t.prices {
		sort.SliceStable(list, func(i, j int) bool { return list[i].At < list[j].At })
	}
	return t
}

// Resolve returns the Profile in effect at the instant: the latest switch at or before it.
// A request before the earliest switch belongs to the earliest switch's Profile. A CLI
// without any switch has an empty Profile; DSH never has one. The multiplier is the one
// recorded by the latest metadata change after that switch, else the switch's own.
func (t *Timeline) Resolve(cli CLI, at time.Time) Attribution {
	out := Attribution{Multiplier: 1}
	list := t.apply[cli]
	if cli == DSH || len(list) == 0 {
		return out
	}
	ns := at.UnixNano()
	i := sort.Search(len(list), func(i int) bool { return list[i].At > ns }) - 1
	if i < 0 {
		if list[0].Kind == "observe" {
			return out
		}
		return list[0].Attribution
	}
	out = list[i].Attribution
	prices := t.prices[out.ProfileID]
	j := sort.Search(len(prices), func(j int) bool { return prices[j].At > ns }) - 1
	if j >= 0 && prices[j].At > list[i].At {
		out.Multiplier, out.Models = prices[j].Multiplier, prices[j].Models
	}
	return out
}

func loadEvents(ctx context.Context, db *sql.DB) ([]Event, error) {
	rows, e := db.QueryContext(ctx, "SELECT event_key,cli,at,profile_id,profile_name,multiplier,kind,model_multipliers,service_source,source_since FROM profile_events WHERE kind IN ('apply','metadata','observe') ORDER BY at,event_key")
	if e != nil {
		return nil, StoreInvalid
	}
	defer func() { _ = rows.Close() }()
	var out []Event
	for rows.Next() {
		var v Event
		var models string
		if e = rows.Scan(&v.Key, &v.CLI, &v.At, &v.ProfileID, &v.ProfileName, &v.Multiplier, &v.Kind, &models, &v.ServiceSource, &v.Since); e != nil {
			return nil, StoreInvalid
		}
		if models != "" && json.Unmarshal([]byte(models), &v.Models) != nil {
			return nil, StoreInvalid
		}
		out = append(out, v)
	}
	if rows.Err() != nil {
		return nil, StoreInvalid
	}
	return out, nil
}

// Timeline brings the switch history up to date and returns a snapshot of it.
func (m *Manager) Timeline(ctx context.Context) (*Timeline, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.repairEvents(ctx); e != nil {
		return nil, e
	}
	if !m.blocked {
		for _, cli := range []CLI{Claude, Codex} {
			b, err := m.inspect(ctx, cli)
			if err != nil {
				return nil, err
			}
			if err = m.observeSource(ctx, b); err != nil {
				return nil, err
			}
		}
	}
	events, e := loadEvents(ctx, m.events)
	if e != nil {
		return nil, e
	}
	return newTimeline(events), nil
}

// Seed says CLI belonged to the Profile named ProfileName from At onwards.
type Seed struct {
	CLI         CLI
	ProfileName string
	At          time.Time
}

// History is the rebuild program's narrow view of the profile store: it reads Profile
// names and the switch history and writes seed events, and touches nothing else.
// Opening it takes the store's instance lock, so it fails while the daemon runs.
type History struct {
	m       *Manager
	scratch string
}

// OpenHistory opens an existing profile store directory. With readOnly the switch
// history is read from a private copy, so even opening it leaves every file untouched.
func OpenHistory(dir string, owner uint32, readOnly bool) (*History, error) {
	m := &Manager{dir: dir, owner: owner, observations: map[CLI]observation{}, plans: map[string]planned{}}
	if e := m.prepare(); e != nil {
		return nil, e
	}
	if e := m.loadProfiles(); e != nil {
		_ = m.Close()
		return nil, e
	}
	h := &History{m: m}
	path := filepath.Join(dir, "profile_events.db")
	if readOnly {
		scratch, e := os.MkdirTemp("", "pax-history-")
		if e != nil {
			_ = m.Close()
			return nil, StoreInvalid
		}
		h.scratch = scratch
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if e = copyPrivate(path+suffix, filepath.Join(scratch, "profile_events.db"+suffix)); e != nil && (suffix == "" || !errors.Is(e, os.ErrNotExist)) {
				_ = h.Close()
				return nil, StoreInvalid
			}
		}
		path = filepath.Join(scratch, "profile_events.db")
	}
	if e := m.openEventsAt(path); e != nil {
		_ = h.Close()
		return nil, e
	}
	return h, nil
}

func copyPrivate(from, to string) error {
	b, e := os.ReadFile(from)
	if e != nil {
		return e
	}
	return os.WriteFile(to, b, 0600)
}

// Close releases the instance lock and removes any private copy.
func (h *History) Close() error {
	e := h.m.Close()
	if h.scratch != "" {
		_ = os.RemoveAll(h.scratch)
	}
	return e
}

// PlanSeeds resolves each Profile by its exact name and returns the seed events the
// history does not hold yet; nothing is written. A missing or ambiguous name fails.
// A seed always sorts before every other event of its CLI, so real switches win.
func (h *History) PlanSeeds(ctx context.Context, seeds []Seed) ([]Event, error) {
	var out []Event
	for _, s := range seeds {
		var found []savedProfile
		for _, p := range h.m.profiles {
			if p.Name == s.ProfileName {
				found = append(found, p)
			}
		}
		if len(found) != 1 {
			return nil, fmt.Errorf("profile %q for %s must exist exactly once, found %d", s.ProfileName, s.CLI, len(found))
		}
		key := "seed:" + string(s.CLI)
		var n int
		if e := h.m.events.QueryRowContext(ctx, "SELECT count(*) FROM profile_events WHERE event_key=?", key).Scan(&n); e != nil {
			return nil, StoreInvalid
		}
		if n > 0 {
			continue
		}
		at := s.At.UnixNano()
		var first sql.NullInt64
		if e := h.m.events.QueryRowContext(ctx, "SELECT min(at) FROM profile_events WHERE cli=? AND kind IN ('apply','observe')", s.CLI).Scan(&first); e != nil {
			return nil, StoreInvalid
		}
		if first.Valid && first.Int64 <= at {
			at = first.Int64 - 1
		}
		p := found[0]
		out = append(out, Event{Key: key, CLI: s.CLI, At: at, Kind: "apply", Attribution: Attribution{ProfileID: p.ID, ProfileName: p.Name, Multiplier: p.Multiplier, Models: p.ModelMultipliers}})
	}
	return out, nil
}

// Write stores events; an existing key is left alone, so repeating a write is harmless.
func (h *History) Write(ctx context.Context, events []Event) error {
	for _, v := range events {
		if e := h.m.recordEvent(ctx, v.Key, v.CLI, v.At, v.Attribution, v.Kind); e != nil {
			return e
		}
	}
	return nil
}

// Timeline loads the stored history plus extra events that are not written yet.
func (h *History) Timeline(ctx context.Context, extra []Event) (*Timeline, error) {
	events, e := loadEvents(ctx, h.m.events)
	if e != nil {
		return nil, e
	}
	return newTimeline(append(events, extra...)), nil
}
