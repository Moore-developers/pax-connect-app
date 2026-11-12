package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/netip"
	"pax-connect/daemon/internal/storage"
	"time"
)

const MeasurementPath = "/Library/Application Support/Pax Connect/route-measurements.json"

// ErrMeasurementDurability indicates a written snapshot whose directory sync failed.
var ErrMeasurementDurability = errors.New("measurement durability uncertain")

var ErrMeasurementStore = errors.New("measurement_store_failed")

// SavedMeasurement holds one latest sample of either kind, without authentication.
type SavedMeasurement struct {
	ExitLocation, LastExitLocation *ExitLocation
	StableMS, StableSamples        uint32
	StableError, TestTarget        string
	Method                         string `json:"method,omitempty"`
	ColdMS, SecondMS               uint32
	Reused                         bool
	FirstError, SecondError        string
	Parallel, SetupMS              uint32
	End                            string
	At                             time.Time `json:"at"`
	ConfigRevision                 uint64    `json:"config_revision"`
	Revision                       uint64    `json:"revision,omitempty"`
	Success                        bool      `json:"success"`
	DurationMS                     uint32    `json:"duration_ms"`
	Error                          string    `json:"error,omitempty"`
	Params                         []string  `json:"params,omitempty"`
	ExitIP                         string    `json:"exit_ip,omitempty"`
	ExitError                      string    `json:"exit_error,omitempty"`
	Bytes                          uint64    `json:"bytes,omitempty"`
	BytesPerSecond                 float64   `json:"bytes_per_second,omitempty"`
}
type MeasurementRecord struct {
	Exit     *SavedMeasurement `json:"exit,omitempty"`
	Latency  *SavedMeasurement `json:"latency,omitempty"`
	Download *SavedMeasurement `json:"download,omitempty"`
}
type MeasurementDocument struct {
	SchemaVersion int                          `json:"schema_version"`
	Records       map[string]MeasurementRecord `json:"records"`
}

func NewMeasurements() MeasurementDocument {
	return MeasurementDocument{SchemaVersion: 2, Records: map[string]MeasurementRecord{}}
}

// MeasurementStore persists only the bounded latest latency/exit and download
// facts. Full paginated history remains a separate ticket36/37 delivery.
type MeasurementStore struct{ file *storage.PrivateFile }

func NewMeasurementStore(path string, owner uint32) *MeasurementStore {
	return &MeasurementStore{file: storage.NewPrivateFile(path, owner, 2*1024*1024)}
}
func (s *MeasurementStore) Load() (MeasurementDocument, error) {
	data, exists, err := s.file.Read()
	if err != nil {
		return MeasurementDocument{}, ErrMeasurementStore
	}
	if !exists {
		return NewMeasurements(), nil
	}
	var doc MeasurementDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&doc) != nil {
		return MeasurementDocument{}, ErrMeasurementStore
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || doc.validate() != nil {
		return MeasurementDocument{}, ErrMeasurementStore
	}
	doc.MigrateExit()
	return doc, nil
}
func (s *MeasurementStore) Save(ctx context.Context, doc MeasurementDocument) error {
	if doc.validate() != nil {
		return ErrMeasurementStore
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return ErrMeasurementStore
	}
	if err := s.file.Write(ctx, data); err != nil {
		if errors.Is(err, storage.ErrCommitUncertain) {
			return ErrMeasurementDurability
		}
		return ErrMeasurementStore
	}
	return nil
}
func (d MeasurementDocument) validate() error {
	if (d.SchemaVersion != 1 && d.SchemaVersion != 2) || d.Records == nil || len(d.Records) > 1000 {
		return ErrMeasurementStore
	}
	for id, record := range d.Records {
		if id == "" || len(id) > 128 {
			return ErrMeasurementStore
		}
		for _, sample := range []*SavedMeasurement{record.Latency, record.Exit, record.Download} {
			if sample == nil {
				continue
			}
			if (sample.ExitLocation != nil && sample.ExitLocation.IP != sample.ExitIP) || !sample.ExitLocation.Valid() || !sample.LastExitLocation.Valid() || sample.At.IsZero() || sample.ConfigRevision == 0 || sample.BytesPerSecond < 0 || math.IsNaN(sample.BytesPerSecond) || math.IsInf(sample.BytesPerSecond, 0) || sample.StableSamples > 3 || len(sample.StableError) > 64 || len(sample.TestTarget) > 32 || len(sample.Error) > 64 || len(sample.ExitError) > 64 || len(sample.Params) > 8 {
				return ErrMeasurementStore
			}
			for _, param := range sample.Params {
				if len(param) > 64 {
					return ErrMeasurementStore
				}
			}
			if sample.ExitIP != "" {
				if _, err := netip.ParseAddr(sample.ExitIP); err != nil {
					return ErrMeasurementStore
				}
			}
		}
	}
	return nil
}
