package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDiagnosticPersistsAndRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diagnostics.jsonl")
	ConfigureDiagnostics(path)
	defer ConfigureDiagnostics("")
	RecordDiagnostic(Diagnostic{Stage: "dns_health", Code: "probe_deadline", BudgetMS: 0})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Diagnostic
	if err = json.Unmarshal(data, &got); err != nil || got.Code != "probe_deadline" || got.Time.IsZero() {
		t.Fatalf("missing observation: %v", err)
	}
	if err = os.WriteFile(path, make([]byte, 1<<20), 0644); err != nil {
		t.Fatal(err)
	}
	RecordDiagnostic(Diagnostic{Stage: "dns_health", Code: "ok", Success: true})
	old, err := os.Stat(path + ".previous")
	if err != nil || old.Size() != 1<<20 {
		t.Fatal("rotation failed")
	}
	current, err := os.Stat(path)
	if err != nil || current.Size() > 1024 {
		t.Fatal("log unbounded")
	}
}
