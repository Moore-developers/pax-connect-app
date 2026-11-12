package controller

import (
	"context"
	"google.golang.org/grpc/status"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/subfetch"
)

type fakeFetcher struct {
	mu      sync.Mutex
	body    []byte
	err     error
	block   bool
	entered chan struct{}
	calls   int
}

func (f *fakeFetcher) set(body string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.body = []byte(body)
	f.err = err
}
func (f *fakeFetcher) FetchSubscription(ctx context.Context, _ string) ([]byte, error) {
	f.mu.Lock()
	f.calls++
	body, err, block, entered := f.body, f.err, f.block, f.entered
	f.mu.Unlock()
	if block {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return body, nil
}

func vlessLink(name, server, uuid, transport string) string {
	return "vless://" + uuid + "@" + server + ":443?encryption=none&security=tls&sni=" + server +
		"&type=" + transport + "&path=%2Fws#" + url.QueryEscape(name)
}

const (
	uuidA = "11111111-1111-1111-1111-111111111111"
	uuidB = "22222222-2222-2222-2222-222222222222"
	uuidC = "33333333-3333-3333-3333-333333333333"
	// subURL 含凭据 query，用于确认它不会出现在产品快照里。
	subURL = "https://sub.example.com/base64?token=SUPERSECRET"
)

func subscriptionBody(links ...string) string { return strings.Join(links, "\n") }

func newSubscriptionController(t *testing.T, fetcher SubscriptionFetcher) (*Controller, *memoryCatalog) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c := New("test", "")
	store := &memoryCatalog{catalog: config.NewCatalog(), exists: true}
	c.ConfigureCatalog(store)
	c.ConfigureOperations(ctx, func() (config.DebugProfile, error) {
		return config.DebugProfile{}, config.ErrProfileUnavailable
	})
	c.ConfigureSubscriptions(fetcher)
	return c, store
}

// operationSink collects what the App would receive, so tests assert the real event seam.
func operationSink(c *Controller) (<-chan *paxv1.OperationResult, func()) {
	queue := make(chan *paxv1.WatchEvent, 64)
	c.mu.Lock()
	c.subscribers[queue] = make(chan struct{})
	c.mu.Unlock()
	results := make(chan *paxv1.OperationResult, 64)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case event := <-queue:
				if payload, ok := event.Payload.(*paxv1.WatchEvent_Operation); ok {
					results <- payload.Operation
				}
			case <-done:
				return
			}
		}
	}()
	return results, func() {
		close(done)
		c.mu.Lock()
		delete(c.subscribers, queue)
		c.mu.Unlock()
	}
}

func awaitResult(t *testing.T, results <-chan *paxv1.OperationResult, id string) *paxv1.OperationResult {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case result := <-results:
			if result.OperationId == id {
				return result
			}
		case <-deadline:
			t.Fatalf("operation %s produced no result", id)
			return nil
		}
	}
}

func TestSubscriptionPreviewConfirmKeepsSourcePrivateAndExcludesUnselected(t *testing.T) {
	fetcher := &fakeFetcher{}
	c, _ := newSubscriptionController(t, fetcher)
	ctx := context.Background()
	fetcher.set(subscriptionBody(vlessLink("香港01", "hk1.example.com", uuidA, "ws"), vlessLink("日本01", "jp1.example.com", uuidB, "ws"), vlessLink("坏线路", "bad.example.com", uuidC, "xhttp")), nil)

	preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: subURL, IsSubscription: true, SourceName: "机场一"})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 3 {
		t.Fatalf("items=%d", len(preview.Items))
	}
	if preview.SourceHost != "sub.example.com" || preview.SourceName != "机场一" {
		t.Fatalf("source not redacted: %+v", preview)
	}
	if preview.SubscriptionId != "" {
		t.Fatal("first import already had a source id")
	}
	// XHTTP 明确排除，不映射成 HTTP/H2。
	if preview.Items[2].ErrorCode != "unsupported_transport_xhttp" {
		t.Fatalf("xhttp item=%+v", preview.Items[2])
	}
	if preview.Items[0].ErrorCode != "" || preview.Items[1].ErrorCode != "" {
		t.Fatal("valid nodes rejected")
	}
	for _, item := range preview.Items {
		if strings.Contains(item.Route.GetServer(), "SUPERSECRET") || strings.Contains(item.Route.GetName(), "token=") {
			t.Fatal("preview leaked the private URL")
		}
	}
	snapshot, _ := c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if strings.Contains(snapshot.String(), "SUPERSECRET") {
		t.Fatal("snapshot leaked the private URL")
	}
	if len(snapshot.Routes) != 0 {
		t.Fatal("preview wrote the catalog")
	}

	confirm, err := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: []string{preview.Items[0].ItemId}})
	if err != nil {
		t.Fatal(err)
	}
	results, stop := operationSink(c)
	defer stop()
	if !awaitResult(t, results, confirm.OperationId).Ok {
		t.Fatal("confirm failed")
	}
	waitComplete(t, c)

	snapshot, _ = c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if len(snapshot.Routes) != 1 {
		t.Fatalf("routes=%d, want only the selected node", len(snapshot.Routes))
	}
	visible, hidden := 0, 0
	for _, route := range snapshot.Routes {
		if route.Origin != config.OriginSubscription || route.SourceId == "" {
			t.Fatalf("origin not recorded: %+v", route)
		}
		if route.Hidden {
			hidden++
		} else {
			visible++
		}
	}
	if visible != 1 || hidden != 0 {
		t.Fatalf("visible=%d hidden=%d", visible, hidden)
	}
	if len(snapshot.Subscriptions) != 1 {
		t.Fatalf("summaries=%d", len(snapshot.Subscriptions))
	}
	summary := snapshot.Subscriptions[0]
	if summary.Name != "机场一" || summary.Host != "sub.example.com" || summary.RouteCount != 1 || summary.HiddenCount != 0 {
		t.Fatalf("summary=%+v", summary)
	}
	if strings.Contains(snapshot.String(), "SUPERSECRET") {
		t.Fatal("snapshot leaked the private URL")
	}

	// 未勾选不创建隐藏记录；后续完整刷新可发现该来源的新节点。
	fetcher.set(subscriptionBody(vlessLink("香港01", "hk1.example.com", uuidA, "ws"), vlessLink("日本01", "jp1.example.com", uuidB, "ws")), nil)
	refresh, err := c.RefreshSubscription(ctx, &paxv1.RefreshSubscriptionRequest{SubscriptionId: summary.Id, ExpectedSourceRevision: summary.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if !awaitResult(t, results, refresh.OperationId).Ok {
		t.Fatal("refresh failed")
	}
	snapshot, _ = c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if len(snapshot.Routes) != 2 {
		t.Fatalf("refresh resurrected or dropped records: %d", len(snapshot.Routes))
	}
	for _, route := range snapshot.Routes {
		if route.Name == "日本01" && route.Hidden {
			t.Fatal("unselected node became a hidden record")
		}
	}
}

func TestRefreshKeepsOldListOnFailureAndPartialBodyPreservesRecords(t *testing.T) {
	fetcher := &fakeFetcher{}
	c, _ := newSubscriptionController(t, fetcher)
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	fetcher.set(subscriptionBody(vlessLink("A", "a.example.com", uuidA, "ws"), vlessLink("B", "b.example.com", uuidB, "ws")), nil)

	preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: subURL, IsSubscription: true})
	if err != nil {
		t.Fatal(err)
	}
	confirm, _ := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: []string{preview.Items[0].ItemId, preview.Items[1].ItemId}})
	awaitResult(t, results, confirm.OperationId)
	waitComplete(t, c)
	snapshot, _ := c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	source := snapshot.Subscriptions[0]
	before := snapshot.Routes

	// 下载失败：保留旧列表，来源带明确错误。
	fetcher.set("", subfetch.ErrFailed)
	failed, err := c.RefreshSubscription(ctx, &paxv1.RefreshSubscriptionRequest{SubscriptionId: source.Id, ExpectedSourceRevision: source.Revision})
	if err != nil {
		t.Fatal(err)
	}
	result := awaitResult(t, results, failed.OperationId)
	if result.Ok || result.Error.GetCode() != "fetch_failed" {
		t.Fatalf("result=%+v", result)
	}
	snapshot, _ = c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if len(snapshot.Routes) != len(before) {
		t.Fatal("failed refresh changed the list")
	}
	if snapshot.Subscriptions[0].LastError.GetCode() != "fetch_failed" {
		t.Fatalf("source error not reported: %+v", snapshot.Subscriptions[0])
	}
	if snapshot.Subscriptions[0].Revision != source.Revision {
		t.Fatal("failed refresh bumped the source revision")
	}

	// 部分坏行：有效映射更新，无法对应的旧记录保留并标部分失败。
	fetcher.set(subscriptionBody(vlessLink("A", "a.example.com", uuidA, "ws"), "vless://broken"), nil)
	partial, err := c.RefreshSubscription(ctx, &paxv1.RefreshSubscriptionRequest{SubscriptionId: source.Id, ExpectedSourceRevision: source.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if !awaitResult(t, results, partial.OperationId).Ok {
		t.Fatal("partial refresh should still complete")
	}
	snapshot, _ = c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if len(snapshot.Routes) != 2 {
		t.Fatalf("partial refresh dropped records: %d", len(snapshot.Routes))
	}
	for _, route := range snapshot.Routes {
		if route.SourceRemoved {
			t.Fatal("partial refresh marked a node removed")
		}
	}
	if !snapshot.Subscriptions[0].LastRefresh.Partial || snapshot.Subscriptions[0].LastRefresh.Kept != 1 {
		t.Fatalf("partial report missing: %+v", snapshot.Subscriptions[0].LastRefresh)
	}

	// 完整正文回收无修改且未运行/引用的缺项，汇总仍保留来源移除数量。
	fetcher.set(subscriptionBody(vlessLink("C", "c.example.com", uuidC, "ws")), nil)
	complete, err := c.RefreshSubscription(ctx, &paxv1.RefreshSubscriptionRequest{SubscriptionId: source.Id, ExpectedSourceRevision: source.Revision})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, complete.OperationId)
	snapshot, _ = c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if len(snapshot.Routes) != 1 {
		t.Fatalf("complete refresh did not reclaim unused records: %d", len(snapshot.Routes))
	}
	removed := 0
	for _, route := range snapshot.Routes {
		if route.SourceRemoved {
			removed++
		}
	}
	if removed != 0 {
		t.Fatalf("unused removed records retained: %d", removed)
	}
	if snapshot.Subscriptions[0].LastRefresh.Removed != 2 {
		t.Fatalf("removed report=%+v", snapshot.Subscriptions[0].LastRefresh)
	}
}

func TestRefreshCancelAndStaleSourceRevision(t *testing.T) {
	fetcher := &fakeFetcher{entered: make(chan struct{})}
	c, _ := newSubscriptionController(t, fetcher)
	ctx := context.Background()
	fetcher.set(subscriptionBody(vlessLink("A", "a.example.com", uuidA, "ws")), nil)
	preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: subURL, IsSubscription: true})
	if err != nil {
		t.Fatal(err)
	}
	results, stop := operationSink(c)
	defer stop()
	confirm, _ := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: []string{preview.Items[0].ItemId}})
	awaitResult(t, results, confirm.OperationId)
	waitComplete(t, c)
	snapshot, _ := c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	source := snapshot.Subscriptions[0]

	if _, err := c.RefreshSubscription(ctx, &paxv1.RefreshSubscriptionRequest{SubscriptionId: source.Id, ExpectedSourceRevision: source.Revision + 7}); err == nil {
		t.Fatal("stale source revision accepted")
	}

	fetcher.mu.Lock()
	fetcher.block = true
	fetcher.mu.Unlock()
	refresh, err := c.RefreshSubscription(ctx, &paxv1.RefreshSubscriptionRequest{SubscriptionId: source.Id, ExpectedSourceRevision: source.Revision})
	if err != nil {
		t.Fatal(err)
	}
	// 同来源重复请求复用同一个进行中的操作。
	again, err := c.RefreshSubscription(ctx, &paxv1.RefreshSubscriptionRequest{SubscriptionId: source.Id, ExpectedSourceRevision: source.Revision})
	if err != nil || again.OperationId != refresh.OperationId {
		t.Fatalf("same-source dedup failed: %v %+v", err, again)
	}
	<-fetcher.entered
	if _, err := c.CancelOperation(ctx, &paxv1.CancelOperationRequest{OperationId: refresh.OperationId}); err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, refresh.OperationId); result.Ok || result.Error.GetCode() != "operation_cancelled" {
		t.Fatalf("cancel result=%+v", result)
	}
	if _, err := c.CancelOperation(ctx, &paxv1.CancelOperationRequest{OperationId: "operation-missing"}); err == nil {
		t.Fatal("unknown operation accepted")
	}
	snapshot, _ = c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if snapshot.Subscriptions[0].LastError.GetCode() != "" {
		t.Fatal("cancelled refresh recorded a source failure")
	}
}

func TestSubscriptionParameterEditAndRenameStayInPlace(t *testing.T) {
	fetcher := &fakeFetcher{}
	c, _ := newSubscriptionController(t, fetcher)
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	fetcher.set(subscriptionBody(vlessLink("香港01", "hk1.example.com", uuidA, "ws")), nil)
	preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: subURL, IsSubscription: true, SourceName: "机场一"})
	if err != nil {
		t.Fatal(err)
	}
	confirm, _ := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: []string{preview.Items[0].ItemId}})
	awaitResult(t, results, confirm.OperationId)
	waitComplete(t, c)
	snapshot, _ := c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	route := snapshot.Routes[0]

	// 只改名称：原位修改，来源映射与版本语义保持不变。App 的真实流程是先取编辑器再回传。
	editor, err := c.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: route.Id})
	if err != nil {
		t.Fatal(err)
	}
	renameReq := editor.Fields
	renameReq.Name = "我的香港"
	renameAck, err := c.UpdateRoute(ctx, renameReq)
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, renameAck.OperationId)
	waitComplete(t, c)
	snapshot, _ = c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if len(snapshot.Routes) != 1 {
		t.Fatal("rename created a second route")
	}
	if snapshot.Routes[0].Name != "我的香港" || snapshot.Routes[0].SourceId == "" {
		t.Fatalf("rename lost the source mapping: %+v", snapshot.Routes[0])
	}
	renamed := snapshot.Routes[0]

	// 改连接参数：原ID保留来源并记录覆盖。
	editor, err = c.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: renamed.Id})
	if err != nil {
		t.Fatal(err)
	}
	editReq := editor.Fields
	editReq.Server = "hk2.example.com"
	editReq.ServerName = "hk2.example.com"
	copied, err := c.UpdateRoute(ctx, editReq)
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, copied.OperationId)
	waitComplete(t, c)
	snapshot, _ = c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if len(snapshot.Routes) != 1 {
		t.Fatalf("edit duplicated route: %d", len(snapshot.Routes))
	}
	subscriptionRoute := snapshot.Routes[0]
	if subscriptionRoute.Id != route.Id || subscriptionRoute.SourceId == "" || subscriptionRoute.Server != "hk2.example.com" {
		t.Fatal("edit lost stable source mapping")
	}

	// 删除订阅线路 → 隐藏并可从隐藏列表恢复，恢复后身份与来源不变。
	hidden, err := c.DeleteRoute(ctx, &paxv1.DeleteRouteRequest{RouteId: subscriptionRoute.Id, ExpectedRevision: subscriptionRoute.Revision})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, hidden.OperationId)
	waitComplete(t, c)
	snapshot, _ = c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	hiddenRoute := findRoute(snapshot.Routes, subscriptionRoute.Id)
	if hiddenRoute == nil || !hiddenRoute.Hidden {
		t.Fatal("subscription delete did not hide")
	}
	restored, err := c.RestoreSubscriptionRoute(ctx, &paxv1.RestoreSubscriptionRouteRequest{RouteId: hiddenRoute.Id, ExpectedRevision: hiddenRoute.Revision})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, restored.OperationId)
	waitComplete(t, c)
	snapshot, _ = c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	restoredRoute := findRoute(snapshot.Routes, subscriptionRoute.Id)
	if restoredRoute == nil || restoredRoute.Hidden || restoredRoute.SourceId != subscriptionRoute.SourceId {
		t.Fatalf("restore=%+v", restoredRoute)
	}
	if snapshot.SelectedRouteId != "" {
		t.Fatal("restore implicitly selected a route")
	}
}

func findRoute(routes []*paxv1.NodeProfile, id string) *paxv1.NodeProfile {
	for _, route := range routes {
		if route.Id == id {
			return route
		}
	}
	return nil
}

func TestSubscriptionCommandsFailWithoutFetcher(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := New("test", "")
	c.ConfigureCatalog(&memoryCatalog{catalog: config.NewCatalog(), exists: true})
	c.ConfigureOperations(ctx, func() (config.DebugProfile, error) {
		return config.DebugProfile{}, config.ErrProfileUnavailable
	})
	if _, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: subURL, IsSubscription: true}); err == nil {
		t.Fatal("subscription preview worked without a fetch boundary")
	}
	if _, err := c.RefreshSubscription(ctx, &paxv1.RefreshSubscriptionRequest{SubscriptionId: "x"}); err == nil {
		t.Fatal("refresh worked without a fetch boundary")
	}
}

func TestAutomaticSubscriptionCreatesGroupAndMergesSameSource(t *testing.T) {
	fetcher := &fakeFetcher{}
	fetcher.set("trojan://old@example.test:443#Provider", nil)
	c, _ := newSubscriptionController(t, fetcher)
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	var sourceID, routeID string
	for i := 0; i < 3; i++ {
		if i == 1 {
			fetcher.set("trojan://new@example.test:443#Provider\nunknown://unsupported", nil)
		}
		preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: subURL, IsSubscription: true, ClientId: "auto"})
		if err != nil {
			t.Fatal(err)
		}
		ack, err := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ClientId: "auto", Automatic: true})
		if err != nil {
			t.Fatal(err)
		}
		if result := awaitResult(t, results, ack.OperationId); !result.Ok {
			t.Fatal(result)
		}
		snapshot := waitComplete(t, c)
		if len(snapshot.Subscriptions) != 1 || len(snapshot.Routes) != 1 {
			t.Fatal("source or route duplicated")
		}
		if i == 0 {
			sourceID = snapshot.Subscriptions[0].Id
			routeID = snapshot.Routes[0].Id
		}
		if snapshot.Subscriptions[0].Id != sourceID || snapshot.Routes[0].Id != routeID || snapshot.Routes[0].SourceId != sourceID {
			t.Fatal("source identity lost")
		}
		if snapshot.Status != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF {
			t.Fatal("import enabled proxy")
		}
	}
	var actual string
	_ = c.query(ctx, func(context.Context) { record, _ := c.routes.catalog.Find(routeID); actual = record.Config.Password })
	if actual != "new" {
		t.Fatal("re-pasted subscription did not update source")
	}
}

func TestAutomaticInvalidSourceCreatesNoEmptyGroup(t *testing.T) {
	c, _ := newSubscriptionController(t, &fakeFetcher{body: []byte("unknown://bad")})
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: subURL, IsSubscription: true})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, Automatic: true})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); result.Ok {
		t.Fatal("invalid source committed")
	}
	snapshot := waitComplete(t, c)
	if len(snapshot.Subscriptions) != 0 || len(snapshot.Routes) != 0 {
		t.Fatal("failed import left empty source")
	}
}

func TestAutomaticSourceUpdatePreservesLocalOverrideAndHiddenRecord(t *testing.T) {
	fetcher := &fakeFetcher{body: []byte("trojan://old@example.test:443#Provider")}
	c, _ := newSubscriptionController(t, fetcher)
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	importSource := func() {
		preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: subURL, IsSubscription: true})
		if err != nil {
			t.Fatal(err)
		}
		ack, err := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, Automatic: true})
		if err != nil {
			t.Fatal(err)
		}
		if result := awaitResult(t, results, ack.OperationId); !result.Ok {
			t.Fatal(result)
		}
	}
	importSource()
	node := waitComplete(t, c).Routes[0]
	ack, err := c.UpdateRoute(ctx, &paxv1.RouteEditRequest{RouteId: node.Id, ExpectedRevision: node.Revision, DocumentJson: `{"type":"trojan","tag":"My name","server":"override.test","server_port":443,"password":"old","tls":{"enabled":true,"server_name":"example.test"}}`})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	fetcher.set("trojan://new@example.test:443#Provider", nil)
	importSource()
	after := waitComplete(t, c).Routes[0]
	if after.Id != node.Id || after.Name != "My name" || after.Server != "override.test" {
		t.Fatal("source refresh erased local override")
	}
	ack, err = c.DeleteRoute(ctx, &paxv1.DeleteRouteRequest{RouteId: after.Id, ExpectedRevision: after.Revision})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	importSource()
	hidden := waitComplete(t, c).Routes[0]
	if !hidden.Hidden || hidden.Id != node.Id {
		t.Fatal("automatic re-paste resurrected hidden item")
	}
}

func TestDeleteSubscriptionRemovesOwnedRoutesAndPrunesGroups(t *testing.T) {
	c, _ := newSubscriptionController(t, &fakeFetcher{body: []byte("trojan://fixture@one.test:443#One\ntrojan://fixture@two.test:443#Two")})
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: subURL, IsSubscription: true})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, Automatic: true})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	before := waitComplete(t, c)
	source := before.Subscriptions[0]
	ack, err = c.AddRoute(ctx, &paxv1.RouteEditRequest{DocumentJson: `{"type":"trojan","tag":"Local","server":"local.test","server_port":443,"password":"fixture","tls":{"enabled":true,"server_name":"local.test"}}`})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	current := waitComplete(t, c)
	local := current.Routes[2]
	ack, err = c.SaveRouteGroups(ctx, &paxv1.SaveRouteGroupsRequest{ExpectedCatalogRevision: current.CatalogRevision, Groups: []*paxv1.RouteGroup{{Id: "g", Name: "Custom", MemberIds: []string{before.Routes[0].Id, local.Id}}}})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	current = waitComplete(t, c)
	request := &paxv1.DeleteSubscriptionRequest{SubscriptionId: source.Id, ExpectedSourceRevision: source.Revision, ExpectedCatalogRevision: current.CatalogRevision, ClientId: "test", RequestId: "delete"}
	ack, err = c.DeleteSubscription(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); !result.Ok {
		t.Fatal(result)
	}
	after := waitComplete(t, c)
	if len(after.Subscriptions) != 0 || len(after.Routes) != 1 || after.Routes[0].Id != local.Id || after.SelectedRouteId != "" || len(after.RouteGroups[0].MemberIds) != 1 || after.RouteGroups[0].MemberIds[0] != local.Id {
		t.Fatal("subscription deletion affected unrelated route or left owned members")
	}
	replay, err := c.DeleteSubscription(ctx, request)
	if err != nil || replay.OperationId != ack.OperationId {
		t.Fatal("delete retry lost terminal identity", err)
	}
}

func TestDeleteSubscriptionRefusesActiveAndCancelsLateRefresh(t *testing.T) {
	fetcher := &fakeFetcher{body: []byte("trojan://fixture@example.test:443#Fixture")}
	c, _ := newSubscriptionController(t, fetcher)
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: subURL, IsSubscription: true})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, Automatic: true})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	snapshot := waitComplete(t, c)
	node := snapshot.Routes[0]
	source := snapshot.Subscriptions[0]
	c.mu.Lock()
	c.current.CurrentRoute = node
	c.current.Status = paxv1.ProtectionStatus_PROTECTION_STATUS_PROTECTED
	c.mu.Unlock()
	request := &paxv1.DeleteSubscriptionRequest{SubscriptionId: source.Id, ExpectedSourceRevision: source.Revision, ExpectedCatalogRevision: snapshot.CatalogRevision, ClientId: "test", RequestId: "active"}
	ack, err = c.DeleteSubscription(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); result.Ok || result.Error.Code != "subscription_in_use" {
		t.Fatal("active deletion interrupted running route")
	}
	c.mu.Lock()
	c.current.CurrentRoute = nil
	c.current.Status = paxv1.ProtectionStatus_PROTECTION_STATUS_OFF
	c.mu.Unlock()
	fetcher.mu.Lock()
	fetcher.block = true
	fetcher.entered = make(chan struct{})
	entered := fetcher.entered
	fetcher.mu.Unlock()
	refresh, err := c.RefreshSubscription(ctx, &paxv1.RefreshSubscriptionRequest{SubscriptionId: source.Id, ExpectedSourceRevision: source.Revision})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	request.RequestId = "delete"
	ack, err = c.DeleteSubscription(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	// Cancellation and deletion terminals can arrive in either order. Do not
	// discard the refresh result while waiting for the deletion terminal.
	remaining := map[string]bool{ack.OperationId: true, refresh.OperationId: true}
	deadline := time.After(5 * time.Second)
	for len(remaining) > 0 {
		select {
		case result := <-results:
			if result.OperationId == ack.OperationId && !result.Ok {
				t.Fatal(result)
			}
			delete(remaining, result.OperationId)
		case <-deadline:
			t.Fatal("deletion/refresh terminals missing")
		}
	}
	after := waitComplete(t, c)
	if len(after.Routes) != 0 || len(after.Subscriptions) != 0 {
		t.Fatal("late refresh resurrected source")
	}
}

type deletePreviewFetcher struct {
	calls   int
	entered chan struct{}
	release chan struct{}
}

func (f *deletePreviewFetcher) FetchSubscription(ctx context.Context, _ string) ([]byte, error) {
	f.calls++
	if f.calls > 1 {
		close(f.entered)
		select {
		case <-f.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return []byte("trojan://fixture@example.test:443#Fixture"), nil
}
func TestDeletionDuringPreviewFetchCannotRecreateSource(t *testing.T) {
	fetcher := &deletePreviewFetcher{entered: make(chan struct{}), release: make(chan struct{})}
	c, _ := newSubscriptionController(t, fetcher)
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: subURL, IsSubscription: true})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, Automatic: true})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	before := waitComplete(t, c)
	source := before.Subscriptions[0]
	done := make(chan error, 1)
	go func() {
		_, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: subURL, IsSubscription: true})
		done <- err
	}()
	<-fetcher.entered
	ack, err = c.DeleteSubscription(ctx, &paxv1.DeleteSubscriptionRequest{SubscriptionId: source.Id, ExpectedSourceRevision: source.Revision, ExpectedCatalogRevision: before.CatalogRevision})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	close(fetcher.release)
	if status.Convert(<-done).Message() != "subscription_stale" {
		t.Fatal("deleted source returned a recreatable late preview")
	}
	after := waitComplete(t, c)
	if len(after.Subscriptions) != 0 || len(after.Routes) != 0 {
		t.Fatal("preview resurrected deletion")
	}
}
