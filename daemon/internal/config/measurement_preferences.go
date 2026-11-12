package config

import (
	"context"
	"encoding/json"
	"errors"
	"pax-connect/daemon/internal/storage"
)

var ErrMeasurementPreferences = errors.New("measurement_preferences_invalid")

// MeasurementPreferences is private daemon-owned configuration, without credentials.
type MeasurementPreferences struct {
	LatencyMode, ExitMode, DownloadMode                      string
	Revision                                                 uint64 `json:"revision"`
	Latency, Exit, Download                                  int
	Mode                                                     string
	RecommendedLatency, RecommendedExit, RecommendedDownload int
	Network                                                  string
	EvaluatedAt                                              int64
}

func (p MeasurementPreferences) Validate() error {
	for _, mode := range []string{p.LatencyMode, p.ExitMode, p.DownloadMode} {
		if mode != "" && mode != "default" && mode != "manual" && mode != "auto" {
			return ErrMeasurementPreferences
		}
	}
	if p.Revision == 0 || p.Latency < 1 || p.Latency > 100 || p.Exit < 1 || p.Exit > 32 || p.Download < 1 || p.Download > 8 {
		return ErrMeasurementPreferences
	}
	if p.Mode != "default" && p.Mode != "manual" && p.Mode != "auto" {
		return ErrMeasurementPreferences
	}
	if p.RecommendedDownload != 0 && (p.RecommendedDownload < 1 || p.RecommendedDownload > 8 || p.RecommendedExit < 0 || p.RecommendedExit > 32 || p.RecommendedLatency < 1 || p.RecommendedLatency > 100 || len(p.Network) > 128) {
		return ErrMeasurementPreferences
	}
	return nil
}

// MeasurementPreferenceStore uses the existing protected atomic file shell.
type MeasurementPreferenceStore struct{ file *storage.PrivateFile }

func NewMeasurementPreferenceStore(path string, owner uint32) *MeasurementPreferenceStore {
	return &MeasurementPreferenceStore{storage.NewPrivateFile(path, owner, 16384)}
}
func (s *MeasurementPreferenceStore) Load() (MeasurementPreferences, bool, error) {
	data, exists, err := s.file.Read()
	if err != nil || !exists {
		return MeasurementPreferences{}, exists, err
	}
	var value MeasurementPreferences
	if json.Unmarshal(data, &value) != nil || value.Validate() != nil {
		return value, true, ErrMeasurementPreferences
	}
	return value, true, nil
}
func (s *MeasurementPreferenceStore) Save(ctx context.Context, p MeasurementPreferences) error {
	if err := p.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.file.Write(ctx, data)
}
