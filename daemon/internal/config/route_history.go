package config

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"pax-connect/daemon/internal/storage"
	"sort"
	"time"
)

// HistorySample contains metric facts only; no document, URL or credentials.
type HistorySample struct {
	ExitLocation                                    *ExitLocation
	RouteID, Metric, Network, Error, ExitIP, Method string
	At                                              time.Time
	ConfigRevision                                  uint64
	Value                                           float64
	Success                                         bool
}
type RouteHistory struct {
	Version  int
	Revision uint64
	Items    []HistorySample
}

func NewRouteHistory() RouteHistory {
	return RouteHistory{Version: 1, Revision: 1, Items: []HistorySample{}}
}
func (h *RouteHistory) Append(v HistorySample, now time.Time) {
	if v.At.IsZero() || v.ConfigRevision == 0 || v.Error == "operation_cancelled" {
		return
	}
	before := h.Revision
	h.Items = append(h.Items, v)
	h.Prune(now)
	if h.Revision == before { // Prune already moved it when it trimmed; a new point is one change either way
		h.Revision++
	}
}

// MetricLatencyAuto is the background sampler's own series; manual latency/connect stay separate.
const MetricLatencyAuto = "latency_auto"

// HistoryLimit is the per-route retention of one metric: the automatic latency series keeps
// about an hour of 30-second samples, every other metric keeps 50.
func HistoryLimit(metric string) int {
	if metric == MetricLatencyAuto {
		return 120
	}
	return 50
}

// Prune enforces the same retention on writes, reloads and read-only queries. Trimming any item
// moves Revision: the visible series changed, and R4 keeps it with Items in the same file.
func (h *RouteHistory) Prune(now time.Time) {
	before := len(h.Items)
	sort.SliceStable(h.Items, func(i, j int) bool { return h.Items[i].At.After(h.Items[j].At) })
	counts := map[string]int{}
	kept := []HistorySample{}
	for _, s := range h.Items {
		key := s.RouteID + "/" + s.Metric
		if now.Sub(s.At) > 7*24*time.Hour || counts[key] >= HistoryLimit(s.Metric) || len(kept) >= 10000 {
			continue
		}
		counts[key]++
		kept = append(kept, s)
	}
	h.Items = kept
	if len(kept) != before {
		h.Revision++
	}
}

type RouteHistoryStore struct{ file *storage.PrivateFile }

func NewRouteHistoryStore(path string, owner uint32) *RouteHistoryStore {
	return &RouteHistoryStore{storage.NewPrivateFile(path, owner, 8<<20)}
}
func (s *RouteHistoryStore) Load() (RouteHistory, error) {
	raw, exists, err := s.file.Read()
	if err != nil {
		return RouteHistory{}, err
	}
	if !exists {
		return NewRouteHistory(), nil
	}
	var h RouteHistory
	if json.Unmarshal(raw, &h) != nil || h.Version != 1 || h.Revision == 0 || len(h.Items) > 10000 {
		return h, errors.New("history_corrupt")
	}
	for _, v := range h.Items {
		if (v.ExitLocation != nil && v.ExitLocation.IP != v.ExitIP) || !v.ExitLocation.Valid() || v.RouteID == "" || v.ConfigRevision == 0 || (v.Metric != "latency" && v.Metric != MetricLatencyAuto && v.Metric != "exit" && v.Metric != "download" && v.Metric != "connect") || v.At.IsZero() || len(v.RouteID) > 128 || len(v.Method) > 64 || len(v.Error) > 64 || len(v.Network) > 128 || len(v.ExitIP) > 64 || math.IsNaN(v.Value) || math.IsInf(v.Value, 0) || v.Value < 0 {
			return h, errors.New("history_corrupt")
		}
	}
	h.Prune(time.Now())
	return h, nil
}
func (s *RouteHistoryStore) Save(ctx context.Context, h RouteHistory) error {
	raw, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return s.file.Write(ctx, raw)
}
