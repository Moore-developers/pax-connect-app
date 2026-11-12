//go:build hostpathdiagnostic

package network

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// Explicit read-only host verification, excluded from offline unit suites.
func TestHostPathDiagnostics(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	iface, err := TunInterface()
	if err != nil {
		t.Fatal(err)
	}
	report := NewPathInspector().Inspect(ctx, iface)
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(data))
}
