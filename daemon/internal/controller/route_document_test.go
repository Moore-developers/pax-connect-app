package controller

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"strings"
	"testing"
	"time"
)

func TestRouteDocumentPreservesFieldsAndPrivateBoundary(t *testing.T) {
	c, _ := newSubscriptionController(t, &fakeFetcher{})
	ctx := context.Background()
	document := `{"type":"hysteria2","tag":"Document","server":"example.test","server_port":443,"password":"PRIVATE-DOCUMENT-SECRET","tls":{"enabled":true,"server_name":"example.test"},"connect_timeout":"7s"}`
	if _, err := c.AddRoute(ctx, &paxv1.RouteEditRequest{DocumentJson: document}); err != nil {
		t.Fatal(err)
	}
	snapshot := waitComplete(t, c)
	if len(snapshot.Routes) != 1 {
		t.Fatalf("document not saved: %v", snapshot)
	}
	if strings.Contains(snapshot.String(), "PRIVATE-DOCUMENT-SECRET") {
		t.Fatal("private document leaked")
	}
	editor, err := c.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: snapshot.Routes[0].Id})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(editor.DocumentJson, `"connect_timeout":"7s"`) || !strings.Contains(editor.DocumentJson, "PRIVATE-DOCUMENT-SECRET") {
		t.Fatal("editor lost document fields")
	}
}

func TestSubscriptionDocumentEditStaysInPlaceAndSurvivesRefresh(t *testing.T) {
	fetcher := &fakeFetcher{}
	c, _ := newSubscriptionController(t, fetcher)
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	fetcher.set(subscriptionBody(vlessLink("Source", "edge.example.test", uuidA, "ws")), nil)
	preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: subURL, IsSubscription: true, SourceName: "Provider"})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: []string{preview.Items[0].ItemId}})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	snapshot := waitComplete(t, c)
	node := snapshot.Routes[0]
	editor, err := c.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: node.Id})
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(editor.DocumentJson, `"server":"edge.example.test"`, `"server":"override.example.test"`, 1)
	ack, err = c.UpdateRoute(ctx, &paxv1.RouteEditRequest{RouteId: node.Id, ExpectedRevision: node.Revision, DocumentJson: changed})
	if err != nil {
		t.Fatal(err)
	}
	result := awaitResult(t, results, ack.OperationId)
	if !result.Ok {
		t.Fatal(result)
	}
	snapshot = waitComplete(t, c)
	if len(snapshot.Routes) != 1 || snapshot.Routes[0].Id != node.Id || snapshot.Routes[0].Server != "override.example.test" || snapshot.Routes[0].SourceId != node.SourceId {
		t.Fatal("edit did not preserve source identity")
	}
	source := snapshot.Subscriptions[0]
	fetcher.set(subscriptionBody(vlessLink("Source", "edge.example.test", uuidB, "ws")), nil)
	ack, err = c.RefreshSubscription(ctx, &paxv1.RefreshSubscriptionRequest{SubscriptionId: source.Id, ExpectedSourceRevision: source.Revision})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	snapshot = waitComplete(t, c)
	if len(snapshot.Routes) != 1 || snapshot.Routes[0].Server != "override.example.test" {
		t.Fatal("refresh erased override")
	}
	editor, err = c.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: node.Id})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(editor.DocumentJson, uuidB) {
		t.Fatal("unedited credentials did not refresh")
	}
}

func TestLegacyCatalogMigratesWithoutLosingIdentity(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "routes.json")
	legacy := `{"schema_version":1,"revision":7,"selected_id":"stable","routes":[{"config":{"id":"stable","name":"Legacy","protocol":"hysteria2","server":"example.test","port":443,"schema_version":1,"password":"secret","server_name":"example.test","transport":"quic","security":"tls"},"revision":3}]}`
	if err = os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := New("migration", "")
	c.ConfigureCatalog(config.NewCatalogStore(path, uint32(os.Getuid())))
	c.ConfigureOperations(ctx, func() (config.DebugProfile, error) { return config.DebugProfile{}, config.ErrProfileUnavailable })
	var snapshot *paxv1.Snapshot
	for i := 0; i < 100; i++ {
		snapshot, _ = c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
		if len(snapshot.Routes) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if snapshot.SelectedRouteId != "stable" || len(snapshot.Routes) != 1 || snapshot.Routes[0].Revision != 3 {
		t.Fatal("migration lost identity")
	}
	editor, err := c.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: "stable"})
	if err != nil || !strings.Contains(editor.DocumentJson, "secret") {
		t.Fatal("migration lost credentials")
	}
	disk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + ".schema1.bak")
	if err != nil || string(backup) != legacy {
		t.Fatal("migration backup missing or changed")
	}
	if !strings.Contains(string(disk), `"schema_version":2`) || strings.Contains(string(disk), `"config":`) {
		t.Fatal("catalog not migrated to single document source")
	}
}

func TestInvalidDocumentsNeverWriteOrLeakValues(t *testing.T) {
	c, _ := newSubscriptionController(t, &fakeFetcher{})
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	base := `{"type":"hysteria2","tag":"Private","server":"example.test","server_port":443,"password":"SECRET","tls":{"enabled":true,"server_name":"example.test"}`
	for _, suffix := range []string{`,"server":"SECRET"}`, `,"detour":"SECRET"}`, `,"unknown_parameter":"SECRET"}`, `,"connect_timeout":"SECRET"}`} {
		ack, err := c.AddRoute(ctx, &paxv1.RouteEditRequest{DocumentJson: base + suffix})
		if err != nil {
			t.Fatal(err)
		}
		result := awaitResult(t, results, ack.OperationId)
		if result.Ok || strings.Contains(result.String(), "SECRET") {
			t.Fatal("invalid document accepted or leaked")
		}
		if len(waitComplete(t, c).Routes) != 0 {
			t.Fatal("invalid document wrote")
		}
	}
}

func TestDocumentRequestIDDoesNotRepeatMutation(t *testing.T) {
	c, _ := newSubscriptionController(t, &fakeFetcher{})
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	req := &paxv1.RouteEditRequest{RequestId: "request-one", DocumentJson: `{"type":"hysteria2","tag":"Once","server":"example.test","server_port":443,"password":"secret","tls":{"enabled":true,"server_name":"example.test"}}`}
	first, err := c.AddRoute(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, first.OperationId)
	for i := 0; i < 257; i++ {
		node := waitComplete(t, c).Routes[0]
		ack, err := c.UpdateRoute(ctx, &paxv1.RouteEditRequest{RouteId: node.Id, ExpectedRevision: node.Revision, Name: "Updated", DocumentJson: req.DocumentJson})
		if err != nil {
			t.Fatal(err)
		}
		awaitResult(t, results, ack.OperationId)
	}
	before := waitComplete(t, c)
	second, err := c.AddRoute(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	replayed := awaitResult(t, results, second.OperationId)
	if !replayed.Ok {
		t.Fatal("retry did not replay successful result")
	}
	if second.OperationId != first.OperationId {
		t.Fatal("duplicate request was admitted twice")
	}
	after, _ := c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if after.CatalogRevision != before.CatalogRevision {
		t.Fatal("duplicate request mutated catalog")
	}
	req.DocumentJson = strings.Replace(req.DocumentJson, "Once", "Different", 1)
	if _, err = c.AddRoute(ctx, req); err == nil {
		t.Fatal("request id reused with different payload")
	}
}

func TestRenameKeepsConnectionVersionAndDocument(t *testing.T) {
	c, _ := newSubscriptionController(t, &fakeFetcher{})
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.AddRoute(ctx, &paxv1.RouteEditRequest{DocumentJson: `{"type":"hysteria2","tag":"Before","server":"example.test","server_port":443,"password":"secret","tls":{"enabled":true,"server_name":"example.test"},"connect_timeout":"7s"}`})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	snapshot := waitComplete(t, c)
	node := snapshot.Routes[0]
	editor, err := c.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: node.Id})
	if err != nil {
		t.Fatal(err)
	}
	ack, err = c.UpdateRoute(ctx, &paxv1.RouteEditRequest{RouteId: node.Id, ExpectedRevision: node.Revision, Name: "After", DocumentJson: editor.DocumentJson})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	snapshot = waitComplete(t, c)
	if snapshot.Routes[0].ConfigRevision != node.ConfigRevision || snapshot.Routes[0].Revision <= node.Revision {
		t.Fatal("rename changed connection version")
	}
	editor, err = c.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: node.Id})
	if err != nil || !strings.Contains(editor.DocumentJson, `"connect_timeout":"7s"`) {
		t.Fatal("rename lost document")
	}
}

func TestMigrationFailureLeavesOriginalFile(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "routes.json")
	original := []byte(`{"schema_version":1,"revision":7,"selected_id":"missing","routes":[]}`)
	if err = os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := New("broken", "")
	c.ConfigureCatalog(config.NewCatalogStore(path, uint32(os.Getuid())))
	c.ConfigureOperations(ctx, func() (config.DebugProfile, error) { return config.DebugProfile{}, config.ErrProfileUnavailable })
	// Query is ordered after initialization; an invalid catalog must not create an editable empty fallback.
	if _, err = c.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: "missing"}); err == nil {
		t.Fatal("invalid catalog accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(original) {
		t.Fatal("failed migration overwrote original")
	}
}

func TestExplicitDocumentRemovalSurvivesSourceRefresh(t *testing.T) {
	fetcher := &fakeFetcher{}
	c, _ := newSubscriptionController(t, fetcher)
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	fetcher.set(subscriptionBody(vlessLink("Source", "edge.example.test", uuidA, "ws")+""), nil)
	preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: subURL, IsSubscription: true, SourceName: "Provider"})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: []string{preview.Items[0].ItemId}})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	snapshot := waitComplete(t, c)
	node := snapshot.Routes[0]
	editor, err := c.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: node.Id})
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if json.Unmarshal([]byte(editor.DocumentJson), &object) != nil {
		t.Fatal("invalid editor")
	}
	transport := object["transport"].(map[string]any)
	delete(transport, "headers")
	raw, _ := json.Marshal(object)
	ack, err = c.UpdateRoute(ctx, &paxv1.RouteEditRequest{RouteId: node.Id, ExpectedRevision: node.Revision, DocumentJson: string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	result := awaitResult(t, results, ack.OperationId)
	if !result.Ok {
		t.Fatal(result)
	}
	source := waitComplete(t, c).Subscriptions[0]
	ack, err = c.RefreshSubscription(ctx, &paxv1.RefreshSubscriptionRequest{SubscriptionId: source.Id, ExpectedSourceRevision: source.Revision})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	editor, err = c.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: node.Id})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(editor.DocumentJson, `"headers"`) {
		t.Fatal("source refresh restored explicitly deleted field")
	}
}

func TestBackupFailurePreventsMigration(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "routes.json")
	legacy := []byte(`{"schema_version":1,"revision":1,"selected_id":"","routes":[]}`)
	if err = os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("absent", path+".schema1.bak"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := New("backup-failure", "")
	c.ConfigureCatalog(config.NewCatalogStore(path, uint32(os.Getuid())))
	c.ConfigureOperations(ctx, func() (config.DebugProfile, error) { return config.DebugProfile{}, config.ErrProfileUnavailable })
	if _, err = c.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: "none"}); err == nil {
		t.Fatal("unexpected editor")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(legacy) {
		t.Fatal("backup failure changed original")
	}
}

func TestDocumentTLSHintRequiresConsentAndIdentityChangeInvalidatesIt(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := New("tls-doc", "")
	c.ConfigureCatalog(&memoryCatalog{catalog: config.NewCatalog(), exists: true})
	c.ConfigureTLS(config.NewTLSPolicyStore(filepath.Join(dir, "tls.json"), uint32(os.Getuid())))
	c.ConfigureOperations(ctx, func() (config.DebugProfile, error) { return config.DebugProfile{}, config.ErrProfileUnavailable })
	results, stop := operationSink(c)
	defer stop()
	raw := `{"type":"hysteria2","tag":"TLS","server":"example.test","server_port":443,"password":"secret","tls":{"enabled":true,"server_name":"example.test","insecure":true}}`
	ack, err := c.AddRoute(ctx, &paxv1.RouteEditRequest{DocumentJson: raw})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	node := waitComplete(t, c).Routes[0]
	if node.TlsPolicy.NeedsConfirmation || node.TlsPolicy.Mode != paxv1.TLSMode_TLS_MODE_SKIP_VERIFY {
		t.Fatal("JSON insecure became consent")
	}
	ack, err = c.UpdateRouteTLS(ctx, &paxv1.UpdateRouteTLSRequest{RouteId: node.Id, IdentityToken: node.TlsPolicy.IdentityToken, ExpectedRevision: node.TlsPolicy.PolicyRevision, Mode: paxv1.TLSMode_TLS_MODE_SKIP_VERIFY, ConfirmSkip: true})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	beforeConsent := node.ConfigRevision
	node = waitComplete(t, c).Routes[0]
	if node.ConfigRevision != beforeConsent {
		t.Fatal("same effective mode unexpectedly changed connection version")
	}
	ack, err = c.UpdateRoute(ctx, &paxv1.RouteEditRequest{RouteId: node.Id, ExpectedRevision: node.Revision, DocumentJson: strings.Replace(raw, `"server":"example.test"`, `"server":"changed.test"`, 1)})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	changed := waitComplete(t, c).Routes[0]
	if changed.TlsPolicy.Mode != paxv1.TLSMode_TLS_MODE_SKIP_VERIFY || changed.TlsPolicy.UserOverride || changed.TlsPolicy.IdentityToken == node.TlsPolicy.IdentityToken {
		t.Fatal("identity change reused old consent")
	}
}
