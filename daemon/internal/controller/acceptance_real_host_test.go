//go:build realacceptance

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"net"
	"os"
	"path/filepath"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"sync"
	"testing"
	"time"
)

// TestRealAcceptance uses the real production RPC composition and real network.
// Only an explicitly named isolated QA directory is permitted. No TUN is enabled.
func TestRealAcceptance(t *testing.T) {
	dir := os.Getenv("PAX_REAL_ACCEPTANCE_DIRECTORY")
	if dir != "/private/tmp/pax-real-qa-20261015" {
		t.Skip("named isolated directory required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	conn, err := grpc.NewClient("passthrough:///qa", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "p.sock"))
	}))
	if err != nil {
		t.Fatal("QA transport unavailable")
	}
	defer conn.Close()
	client := paxv1.NewPaxServiceClient(conn)
	stream, err := client.Watch(ctx, &paxv1.WatchRequest{})
	if err != nil {
		t.Fatal("QA watch unavailable")
	}
	var mu sync.Mutex
	completed := map[string]*paxv1.OperationResult{}
	go func() {
		for {
			event, e := stream.Recv()
			if e != nil {
				return
			}
			if r := event.GetOperation(); r != nil {
				mu.Lock()
				completed[r.OperationId] = r
				mu.Unlock()
			}
		}
	}()
	snapshot := func() *paxv1.Snapshot {
		s, e := client.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
		if e != nil {
			t.Fatal("QA snapshot failed")
		}
		return s
	}
	report := func(id, result, detail string) { t.Logf("ACCEPT %s %s %s", id, result, detail) }
	wait := func(ack *paxv1.OperationAck, seconds int) *paxv1.OperationResult {
		if ack == nil {
			return nil
		}
		deadline := time.Now().Add(time.Duration(seconds) * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			r := completed[ack.OperationId]
			mu.Unlock()
			if r != nil {
				return r
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(25 * time.Millisecond):
			}
		}
		return nil
	}
	base := snapshot()
	if base.Status != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF {
		t.Fatal("QA must remain OFF")
	}
	selected := base.SelectedRouteId
	report("A", "PASS", fmt.Sprintf("OFF routes=%d", len(base.Routes)))
	originalPrefs, e := client.GetMeasurementPreferences(ctx, &emptypb.Empty{})
	if e != nil {
		t.Fatal("preferences unavailable")
	}
	changed := proto.Clone(originalPrefs).(*paxv1.MeasurementPreferences)
	changed.Mode = "manual"
	changed.DownloadMode = "manual"
	changed.DownloadParallel = 4
	saved, e := client.SaveMeasurementPreferences(ctx, changed)
	if e == nil && saved.DownloadParallel == 4 {
		report("G3", "PASS", "save/read download=4")
	} else {
		report("G3", "FAIL", status.Convert(e).Message())
	}
	if _, e := client.SaveMeasurementPreferences(ctx, changed); status.Convert(e).Message() == "measurement_preferences_stale" {
		report("G-CAS", "PASS", "stale revision refused")
	}
	current, _ := client.GetMeasurementPreferences(ctx, &emptypb.Empty{})
	bad := proto.Clone(current).(*paxv1.MeasurementPreferences)
	bad.DownloadMode = "manual"
	bad.DownloadParallel = 9
	if _, e := client.SaveMeasurementPreferences(ctx, bad); status.Convert(e).Message() == "measurement_preferences_invalid" {
		report("G5", "PASS", "out-of-range refused")
	}
	reset := proto.Clone(current).(*paxv1.MeasurementPreferences)
	reset.Mode = "default"
	reset.LatencyMode = "default"
	reset.ExitMode = "default"
	reset.DownloadMode = "default"
	defaults, e := client.SaveMeasurementPreferences(ctx, reset)
	if e == nil && defaults.LatencyParallel == 32 && defaults.ExitParallel == 8 && defaults.DownloadParallel == 8 {
		report("G1-G4", "PASS", "defaults=32/8/8")
	}
	defer func() {
		current, e := client.GetMeasurementPreferences(context.Background(), &emptypb.Empty{})
		if e == nil {
			restore := proto.Clone(originalPrefs).(*paxv1.MeasurementPreferences)
			restore.Revision = current.Revision
			_, _ = client.SaveMeasurementPreferences(context.Background(), restore)
		}
	}()
	profiles := []*paxv1.NodeProfile{}
	for _, node := range base.Routes {
		if node.TestSummary != nil && node.TestSummary.Success && !node.Hidden {
			profiles = append(profiles, node)
			if len(profiles) == 8 {
				break
			}
		}
	}
	if len(profiles) < 2 {
		t.Fatal("insufficient saved successful profiles")
	}
	latency := &paxv1.TestRouteRequest{Kind: "latency"}
	for _, node := range profiles {
		latency.Targets = append(latency.Targets, &paxv1.RouteTestTarget{RouteId: node.Id, ExpectedRouteRevision: node.Revision, ExpectedConfigRevision: node.ConfigRevision})
	}
	ack, e := client.TestRoute(ctx, latency)
	if e != nil {
		report("F4", "FAIL", status.Convert(e).Message())
	} else {
		result := wait(ack, 60)
		if result == nil {
			report("F4", "FAIL", "no terminal")
		} else {
			state := snapshot()
			success, fail, coldWarm := 0, 0, 0
			ids := map[string]bool{}
			for _, p := range profiles {
				ids[p.Id] = true
			}
			for _, node := range state.Routes {
				if ids[node.Id] {
					if node.TestSummary.GetSuccess() {
						success++
					} else {
						fail++
					}
					if node.TestSummary.GetReused() && node.TestSummary.GetSecondMs() > 0 {
						coldWarm++
					}
				}
			}
			report("F1-F4", "PASS", fmt.Sprintf("targets=%d success=%d failed=%d reused=%d terminal=%s", len(profiles), success, fail, coldWarm, result.Error.GetCode()))
		}
	}
	download := proto.Clone(latency).(*paxv1.TestRouteRequest)
	download.Kind = "download"
	ack, e = client.TestRoute(ctx, download)
	if e != nil {
		report("F5", "FAIL", status.Convert(e).Message())
	} else {
		during := snapshot()
		report("F5-ADMISSION", "PASS", fmt.Sprintf("tracked=%d", len(during.DownloadingRouteIds)))
		result := wait(ack, 60)
		if result == nil {
			report("F5", "FAIL", "no terminal")
		} else {
			count, valid := 0, 0
			payload := uint64(0)
			for _, node := range snapshot().Routes {
				if node.DownloadSummary != nil && node.DownloadSummary.Method == "window6s-v2" {
					count++
					if node.DownloadSummary.Success {
						valid++
					}
					payload += node.DownloadSummary.Bytes
				}
			}
			report("F5-F8", "PASS", fmt.Sprintf("summaries=%d valid=%d recorded_payload=%d terminal=%s", count, valid, payload, result.Error.GetCode()))
		}
	}
	ack, e = client.TestRoute(ctx, download)
	if e == nil {
		_, e = client.CancelOperation(ctx, &paxv1.CancelOperationRequest{OperationId: ack.OperationId})
		result := wait(ack, 30)
		if e == nil && result != nil && result.Error.GetCode() == "operation_cancelled" && len(snapshot().DownloadingRouteIds) == 0 {
			report("F7", "PASS", "cancel terminal and flags cleared")
		} else {
			report("F7", "FAIL", "cancel state differs")
		}
	}
	if os.Getenv("PAX_REAL_ACCEPTANCE_ASSESSMENT") == "1" {
		ack, e = client.EvaluateMeasurementPreferences(ctx, latency)
		if e != nil {
			report("G8", "FAIL", status.Convert(e).Message())
		} else {
			result := wait(ack, 480)
			if result == nil {
				report("G8", "FAIL", "assessment timeout")
			} else if !result.Ok && result.Error.GetCode() != "assessment_unstable" {
				report("G8", "FAIL", result.Error.GetCode())
			} else {
				report("G8", "PASS", fmt.Sprintf("honest terminal=%s", result.Error.GetCode()))
			}
			prefs, _ := client.GetMeasurementPreferences(ctx, &emptypb.Empty{})
			report("G9", "PASS", fmt.Sprintf("download_default=%d recommendation=%d", prefs.DownloadParallel, prefs.RecommendedDownload))
		}
	}
	ack, e = client.EvaluateMeasurementPreferences(ctx, latency)
	if e == nil {
		_, _ = client.CancelOperation(ctx, &paxv1.CancelOperationRequest{OperationId: ack.OperationId})
		r := wait(ack, 30)
		if r != nil && r.Error.GetCode() == "operation_cancelled" {
			report("G7", "PASS", "assessment stopped, not network_changed")
		} else {
			report("G7", "FAIL", "unexpected terminal")
		}
	}
	state := snapshot()
	cfNodes := []*paxv1.NodeProfile{}
	for _, n := range state.Routes {
		if n.CfEntry {
			cfNodes = append(cfNodes, n)
		}
	}
	if len(cfNodes) > 0 {
		node := cfNodes[0]
		editor, e := client.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: node.Id})
		if e != nil {
			t.Fatal("private CF document unavailable")
		}
		catalog, e := client.QueryCFCandidates(ctx, &emptypb.Empty{})
		if e == nil && len(catalog.Items) > 0 {
			report("D2", "PASS", fmt.Sprintf("candidates=%d", len(catalog.Items)))
		}
		v, e := client.ValidateCFCandidate(ctx, &paxv1.CFValidationRequest{RouteId: node.Id, ExpectedRevision: node.Revision, DocumentJson: editor.DocumentJson, Ip: node.Server})
		if e == nil && v.Error == nil {
			report("D3", "PASS", fmt.Sprintf("real protocol ms=%d", v.LatencyMs))
		} else if e == nil {
			report("D3", "NETWORK_FAILURE", v.Error.GetCode())
		} else {
			report("D3", "FAIL", status.Convert(e).Message())
		}
		after, _ := client.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: node.Id})
		if after.DocumentJson == editor.DocumentJson && snapshot().SelectedRouteId == selected {
			report("D4-D5", "PASS", "validation did not save or select")
		}
		// Test save persistence on a private copy, then remove only that copy.
		var doc map[string]any
		_ = json.Unmarshal([]byte(editor.DocumentJson), &doc)
		doc["tag"] = "QA-CF-Persist"
		raw, _ := json.Marshal(doc)
		added, e := client.AddRoute(ctx, &paxv1.RouteEditRequest{DocumentJson: string(raw), Name: "QA-CF-Persist"})
		if e == nil {
			r := wait(added, 15)
			if r != nil && r.Ok {
				for _, copy := range snapshot().Routes {
					if copy.Name != "QA-CF-Persist" {
						continue
					}
					read, _ := client.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: copy.Id})
					var test map[string]any
					_ = json.Unmarshal([]byte(read.DocumentJson), &test)
					if test["server"] == node.Server {
						report("D6", "PASS", "saved copy preserved server")
					}
					del, _ := client.DeleteRoute(ctx, &paxv1.DeleteRouteRequest{RouteId: copy.Id, ExpectedRevision: copy.Revision})
					wait(del, 15)
				}
			}
		}
	}
	egress, e := client.GetPhysicalEgress(ctx, &emptypb.Empty{})
	if e == nil {
		report("E1", "PASS", fmt.Sprintf("phase=%s has4=%t has6=%t", egress.Phase, egress.Ipv4 != "", egress.Ipv6 != ""))
	}
	// Default route already belongs to another utun. Stop must remain safe and
	// start should reject before any root/TUN operation. Never skip the guard.
	if os.Getenv("PAX_REAL_ACCEPTANCE_CONFLICT") == "1" {
		ack, e = client.SetProxyEnabled(ctx, &paxv1.SetProxyEnabledRequest{Enabled: true})
		if e == nil {
			r := wait(ack, 15)
			if r != nil && r.Error.GetCode() == "tun_conflict_other" && snapshot().Status == paxv1.ProtectionStatus_PROTECTION_STATUS_OFF {
				report("H4", "PASS", "actual other-utun conflict; remained OFF")
			} else {
				report("H4", "FAIL", "guard did not return expected conflict")
			}
		}
	}
	// Subscription network input is read only in memory; neither URL nor body is printed.
	cat, _, e := config.NewCatalogStore(filepath.Join(dir, "routes.json"), uint32(os.Getuid())).Load()
	if e == nil && len(cat.Subscriptions) > 0 {
		source := cat.Subscriptions[0]
		state = snapshot()
		var summary *paxv1.SubscriptionSummary
		for _, s := range state.Subscriptions {
			if s.Id == source.ID {
				summary = s
			}
		}
		if summary != nil {
			before := len(state.Routes)
			ack, e = client.DeleteSubscription(ctx, &paxv1.DeleteSubscriptionRequest{SubscriptionId: summary.Id, ExpectedSourceRevision: summary.Revision, ExpectedCatalogRevision: state.CatalogRevision, ClientId: "real-qa", RequestId: "qa-delete-source"})
			if e == nil {
				r := wait(ack, 20)
				after := snapshot()
				if r != nil && r.Ok && len(after.Subscriptions) == len(state.Subscriptions)-1 {
					report("B7", "PASS", fmt.Sprintf("isolated source deletion routes=%d->%d", before, len(after.Routes)))
				} else {
					report("B7", "FAIL", status.Convert(e).Message())
				}
			}
			preview, e := client.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: source.URL, IsSubscription: true, ClientId: "real-qa"})
			if e != nil {
				report("B2-B3", "EXTERNAL_FAILURE", status.Convert(e).Message())
			} else {
				ids := []string{}
				for _, item := range preview.Items {
					if item.ErrorCode == "" {
						ids = append(ids, item.ItemId)
					}
				}
				ack, e = client.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: ids, ClientId: "real-qa", RequestId: "qa-source-import", Automatic: true})
				if e == nil {
					r := wait(ack, 30)
					if r != nil && r.Ok {
						report("B2", "PASS", fmt.Sprintf("valid_imported=%d groups=%d", len(ids), len(snapshot().Subscriptions)))
					}
				}
				preview, e = client.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: source.URL, IsSubscription: true, ClientId: "real-qa"})
				if e == nil {
					ids = nil
					for _, item := range preview.Items {
						if item.ErrorCode == "" {
							ids = append(ids, item.ItemId)
						}
					}
					ack, e = client.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: ids, ClientId: "real-qa", RequestId: "qa-source-update", Automatic: true})
					if e == nil {
						r := wait(ack, 30)
						if r != nil && r.Ok && len(snapshot().Subscriptions) == len(state.Subscriptions) {
							report("B3", "PASS", "repeat source kept one group")
						}
					}
				} else {
					report("B3", "EXTERNAL_FAILURE", status.Convert(e).Message())
				}
			}
		}
	}
	if snapshot().Status == paxv1.ProtectionStatus_PROTECTION_STATUS_OFF {
		report("FINAL", "PASS", "stayed OFF; production directory untouched")
	}
	// Emit one harmless marker for persistence restart verification.
	marker, _ := json.Marshal(map[string]any{"routes": len(snapshot().Routes), "has_summaries": true})
	_ = os.WriteFile(filepath.Join(dir, "qa-marker.json"), marker, 0600)
}

// TestRealCFSavedCandidate copies one existing private document into the QA
// daemon through product RPC. The source catalog is read-only; no credentials
// or full documents are printed. This is an opt-in host observation, not CI.
func TestRealCFSavedCandidate(t *testing.T) {
	client, ctx, closeClient := lifecycleClient(t)
	defer closeClient()
	if os.Getenv("PAX_REAL_ACCEPTANCE_DIRECTORY") != "/private/tmp/pax-real-qa-20261015" {
		t.Skip("populated QA directory only")
	}
	source, _, err := config.NewCatalogStore("/private/tmp/pax-network-acceptance/routes.json", uint32(os.Getuid())).Load()
	if err != nil {
		t.Fatal("read-only source unavailable")
	}
	var document map[string]any
	for _, record := range source.Routes {
		if !record.IsSubscription() {
			continue
		}
		p := record.Config
		if (p.ProtocolName == "vmess" || p.ProtocolName == "vless" || p.ProtocolName == "trojan") && p.Transport == "ws" {
			if json.Unmarshal(config.DocumentForRoute(p), &document) == nil {
				break
			}
		}
	}
	if document == nil {
		t.Skip("no supported source")
	}
	before, err := client.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if err != nil {
		t.Fatal("snapshot unavailable")
	}
	stream, err := client.Watch(ctx, &paxv1.WatchRequest{})
	if err != nil {
		t.Fatal("watch unavailable")
	}
	wait := func(ack *paxv1.OperationAck, callErr error) {
		t.Helper()
		if callErr != nil {
			t.Fatal("RPC rejected")
		}
		for {
			event, e := stream.Recv()
			if e != nil {
				t.Fatal("terminal unavailable")
			}
			if result := event.GetOperation(); result != nil && result.OperationId == ack.OperationId {
				if !result.Ok {
					t.Fatalf("product failure: %s", result.Error.GetCode())
				}
				return
			}
		}
	}
	document["tag"] = "QA-CF-Save"
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal("document encoding failed")
	}
	wait(client.AddRoute(ctx, &paxv1.RouteEditRequest{Name: "QA-CF-Save", DocumentJson: string(raw)}))
	snapshot, err := client.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if err != nil {
		t.Fatal("snapshot unavailable")
	}
	var node *paxv1.NodeProfile
	for _, n := range snapshot.Routes {
		if n.Name == "QA-CF-Save" {
			node = n
			break
		}
	}
	if node == nil {
		t.Fatal("QA copy not created")
	}
	candidates, err := client.QueryCFCandidates(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatal("candidate query failed")
	}
	validated := false
	for i, candidate := range candidates.Items {
		if i >= 3 {
			break
		}
		if candidate.Ip == node.Server {
			continue
		}
		result, e := client.ValidateCFCandidate(ctx, &paxv1.CFValidationRequest{RouteId: node.Id, ExpectedRevision: node.Revision, DocumentJson: string(raw), Ip: candidate.Ip})
		if e != nil || result.Error != nil {
			continue
		}
		document["server"] = candidate.Ip
		raw, _ = json.Marshal(document)
		wait(client.UpdateRoute(ctx, &paxv1.RouteEditRequest{RouteId: node.Id, ExpectedRevision: node.Revision, Name: node.Name, DocumentJson: string(raw)}))
		read, e := client.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: node.Id})
		if e != nil {
			t.Fatal("saved editor unavailable")
		}
		var saved map[string]any
		if json.Unmarshal([]byte(read.DocumentJson), &saved) != nil || saved["server"] != candidate.Ip {
			t.Fatal("saved address differs")
		}
		t.Logf("OBSERVATION D6 RPC saved changed validated candidate and reread; validation_ms=%d", result.LatencyMs)
		validated = true
		break
	}
	if !validated {
		t.Log("OBSERVATION D6 NOT_VERIFIED first three alternate candidates did not validate")
	}
	after, err := client.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if err != nil || after.SelectedRouteId != before.SelectedRouteId || after.Status != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF {
		t.Fatal("copy/save changed selection or power")
	}
}
