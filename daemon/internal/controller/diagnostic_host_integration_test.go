//go:build hostdiagnostic

package controller

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"pax-connect/daemon/internal/singbox"
	"testing"
	"time"
)

// Explicit read-only host check. Only three saved samples are probed without
// TUN. Print stable protocol/status values, never private documents or names.
func TestSavedRouteDiagnostics(t *testing.T) {
	directory := os.Getenv("PAX_DIAGNOSTIC_DIRECTORY")
	if directory == "" {
		t.Skip("explicit host diagnostic only")
	}
	catalog, exists, err := config.NewCatalogStore(filepath.Join(directory, "routes.json"), uint32(os.Getuid())).Load()
	if err != nil || !exists {
		t.Fatal("private catalog unavailable")
	}
	saved, err := config.NewMeasurementStore(filepath.Join(directory, "route-measurements.json"), uint32(os.Getuid())).Load()
	if err != nil {
		t.Fatal("measurement summaries unavailable")
	}
	chosen := map[string]config.RouteRecord{}
	for _, record := range catalog.Routes {
		summary := saved.Records[record.Config.ID].Latency
		if summary == nil {
			continue
		}
		label := ""
		switch {
		case record.Config.ProtocolName == "vless" && record.Config.Transport == "ws" && !summary.Success:
			label = "failed-vless-ws"
		case record.Config.ProtocolName == "anytls" && !summary.Success:
			label = "failed-anytls"
		case summary.Success && record.Config.ProtocolName == "anytls":
			label = "control-anytls"
		}
		if label != "" {
			if _, ok := chosen[label]; !ok {
				chosen[label] = record
			}
		}
	}
	for _, label := range []string{"failed-vless-ws", "failed-anytls", "control-anytls"} {
		record, ok := chosen[label]
		if !ok {
			continue
		}
		c := newTestController(t, singbox.NewMeasurements(measurement.NewExitLookup()))
		results, stop := operationSink(c)
		ack, err := c.AddRoute(context.Background(), &paxv1.RouteEditRequest{DocumentJson: string(config.DocumentForRoute(record.Config))})
		if err != nil {
			t.Fatal("diagnostic fixture admission failed")
		}
		if !awaitResult(t, results, ack.OperationId).Ok {
			t.Fatal("diagnostic fixture rejected")
		}
		node := waitComplete(t, c).Routes[0]
		ack, err = c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: node.Id, ExpectedRouteRevision: node.Revision, Kind: "diagnostic"})
		if err != nil {
			t.Fatal("diagnostic not admitted")
		}
		// Public task budget is fifteen seconds; this harness waits boundedly.
		timeout := time.After(17 * time.Second)
		for {
			select {
			case result := <-results:
				if result.OperationId != ack.OperationId {
					continue
				}
				output, _ := json.Marshal(map[string]any{"sample": label, "ok": result.Ok, "code": result.Error.GetCode(), "reason": result.Error.GetMessageParams()})
				t.Log(string(output))
				goto finished
			case <-timeout:
				t.Fatal("diagnostic exceeded budget")
			}
		}
	finished:
		stop()
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := c.CloseProxy(shutdown); err != nil {
			t.Fatal(err)
		}
		cancel()
	}
}
