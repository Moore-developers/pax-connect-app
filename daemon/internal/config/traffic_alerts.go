package config

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"pax-connect/daemon/internal/storage"
)

// TrafficAlertRule compares each transfer direction independently, in bytes.
type TrafficAlertRule struct {
	Enabled  bool   `json:"enabled"`
	Upload   uint64 `json:"upload"`
	Download uint64 `json:"download"`
}

// TrafficAlertSettings is daemon-owned intent, independent of route selection.
type TrafficAlertSettings struct {
	Revision      uint64           `json:"revision"`
	Enabled       bool             `json:"enabled"`
	MediumSeconds uint32           `json:"medium_seconds"`
	Short         TrafficAlertRule `json:"short"`
	Hour          TrafficAlertRule `json:"hour"`
	TwoHours      TrafficAlertRule `json:"two_hours"`
	Day           TrafficAlertRule `json:"day"`
}

// DefaultTrafficAlertSettings pre-fills trial thresholds without enabling monitoring.
func DefaultTrafficAlertSettings() TrafficAlertSettings {
	return TrafficAlertSettings{Revision: 1, MediumSeconds: 3600,
		Short:    TrafficAlertRule{true, 100_000_000, 250_000_000},
		Hour:     TrafficAlertRule{true, 500_000_000, 1_000_000_000},
		TwoHours: TrafficAlertRule{true, 1_000_000_000, 2_000_000_000},
		Day:      TrafficAlertRule{true, 2_000_000_000, 5_000_000_000}}
}

// Validate checks saved intent and the external settings RPC boundary.
func (s TrafficAlertSettings) Validate() error {
	if s.Revision == 0 || s.MediumSeconds != 3600 && s.MediumSeconds != 7200 {
		return errors.New("traffic_alerts_invalid")
	}
	for _, r := range []TrafficAlertRule{s.Short, s.Hour, s.TwoHours, s.Day} {
		if r.Upload == 0 || r.Download == 0 {
			return errors.New("traffic_alerts_invalid")
		}
	}
	return nil
}

// TrafficAlertNotice is the immutable crossing summary. It contains no target evidence.
type TrafficAlertNotice struct {
	ID            string    `json:"id"`
	Key           string    `json:"key"`
	Source        string    `json:"source"`
	SourceKind    string    `json:"source_kind"`
	Direction     string    `json:"direction"`
	WindowSeconds uint32    `json:"window_seconds"`
	Bytes         uint64    `json:"bytes"`
	Threshold     uint64    `json:"threshold"`
	Until         time.Time `json:"until"`
	RouteID       string    `json:"route_id"`
	RouteName     string    `json:"route_name"`
	RouteCount    uint32    `json:"route_count"`
	Partial       bool      `json:"partial"`
}

// TrafficAlertEpisode survives transport/process restarts, until confirmed recovery.
type TrafficAlertEpisode struct {
	DayNotified bool               `json:"day_notified"`
	Latest      TrafficAlertNotice `json:"latest"`
}

// TrafficAlertRuntime retains only active episodes under a specific settings revision.
type TrafficAlertRuntime struct {
	Revision uint64                         `json:"revision"`
	Episodes map[string]TrafficAlertEpisode `json:"episodes"`
}

// TrafficAlertStore owns small private settings and active-state files, not a history database.
type TrafficAlertStore struct{ settings, runtime *storage.PrivateFile }

func NewTrafficAlertStore(directory string, owner uint32) *TrafficAlertStore {
	return &TrafficAlertStore{
		storage.NewPrivateFile(filepath.Join(directory, "traffic-alert-settings.json"), owner, 4096),
		storage.NewPrivateFile(filepath.Join(directory, "traffic-alert-state.json"), owner, 2<<20)}
}

func (s *TrafficAlertStore) LoadSettings() (TrafficAlertSettings, error) {
	b, ok, err := s.settings.Read()
	if err != nil || !ok {
		return DefaultTrafficAlertSettings(), err
	}
	var value TrafficAlertSettings
	if json.Unmarshal(b, &value) != nil || value.Validate() != nil {
		return value, errors.New("traffic_alerts_invalid")
	}
	return value, nil
}

func (s *TrafficAlertStore) SaveSettings(ctx context.Context, value TrafficAlertSettings) error {
	if err := value.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.settings.Write(ctx, b)
}

func (s *TrafficAlertStore) LoadRuntime() (TrafficAlertRuntime, error) {
	b, ok, err := s.runtime.Read()
	if err != nil || !ok {
		return TrafficAlertRuntime{}, err
	}
	var value TrafficAlertRuntime
	if json.Unmarshal(b, &value) != nil {
		return value, errors.New("traffic_alerts_state_invalid")
	}
	for key, e := range value.Episodes {
		n := e.Latest
		if n.Key != key || n.ID == "" || n.Threshold == 0 || n.Until.IsZero() || (n.Direction != "upload" && n.Direction != "download") || (n.WindowSeconds != 300 && n.WindowSeconds != 3600 && n.WindowSeconds != 7200 && n.WindowSeconds != 86400) {
			return value, errors.New("traffic_alerts_state_invalid")
		}
	}
	return value, nil
}

func (s *TrafficAlertStore) SaveRuntime(ctx context.Context, value TrafficAlertRuntime) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.runtime.Write(ctx, b)
}
