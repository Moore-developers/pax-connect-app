package config

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestMigrateIndependentExit(t *testing.T) {
	at := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	geo := &ExitLocation{IP: "203.0.113.7", At: at.Add(-time.Minute), ConfigRevision: 7}
	doc := MeasurementDocument{SchemaVersion: 1, Records: map[string]MeasurementRecord{
		"r": {Latency: &SavedMeasurement{At: at, ConfigRevision: 7, Success: true, DurationMS: 42, ExitIP: geo.IP, ExitLocation: geo}},
	}}
	doc.MigrateExit()
	r := doc.Records["r"]
	if r.Exit == nil || r.Exit.ExitIP != geo.IP || r.Exit.At != geo.At || r.Exit.ConfigRevision != 7 || r.Latency.DurationMS != 42 || r.Latency.ExitIP != "" || r.Latency.ExitLocation != nil {
		t.Fatalf("migration lost or coupled facts: %+v", r)
	}
	before := *r.Exit
	doc.MigrateExit()
	if !reflect.DeepEqual(before, *doc.Records["r"].Exit) {
		t.Fatal("migration not idempotentent")
	}
	replacement := &SavedMeasurement{At: at, ConfigRevision: 8, ExitIP: "203.0.113.8", Success: true}
	doc.Records["r"] = MeasurementRecord{Latency: &SavedMeasurement{At: at, ConfigRevision: 7, ExitIP: geo.IP}, Exit: replacement}
	doc.MigrateExit()
	if doc.Records["r"].Exit != replacement {
		t.Fatal("overwrote independent exit")
	}
	if err := doc.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestIndependentMeasurementsRoundTrip(t *testing.T) {
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	store := NewMeasurementStore(filepath.Join(directory, "measurements.json"), uint32(os.Getuid()))
	at := time.Now().UTC()
	legacy := MeasurementDocument{SchemaVersion: 1, Records: map[string]MeasurementRecord{
		"r": {Latency: &SavedMeasurement{At: at, ConfigRevision: 7, Success: true, DurationMS: 42, ExitIP: "203.0.113.9", ExitLocation: &ExitLocation{IP: "203.0.113.9", At: at}}, Download: &SavedMeasurement{At: at, ConfigRevision: 7, Success: true, Bytes: 1048576, BytesPerSecond: 524288}},
	}}
	if err := store.Save(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}
	migrated, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	record := migrated.Records["r"]
	if migrated.SchemaVersion != 2 || record.Exit == nil || record.Exit.ExitIP != "203.0.113.9" || record.Latency.DurationMS != 42 || record.Latency.ExitIP != "" || record.Download.Bytes != 1048576 {
		t.Fatal("migration lost independent facts")
	}
	if err = store.Save(context.Background(), migrated); err != nil {
		t.Fatal(err)
	}
	again, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(migrated, again) {
		t.Fatal("independent roundtrip changed facts")
	}
}
