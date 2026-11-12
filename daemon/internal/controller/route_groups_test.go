package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"strings"
	"testing"
	"time"
)

func TestGroupSaveIsAtomicAndDoesNotChangeRouteVersion(t *testing.T) {
	c, _ := newSubscriptionController(t, &fakeFetcher{})
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.AddRoute(ctx, &paxv1.RouteEditRequest{DocumentJson: `{"type":"trojan","tag":"Group fixture","server":"example.test","server_port":443,"password":"fixture","tls":{"enabled":true,"server_name":"example.test"}}`})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); !result.Ok {
		t.Fatal(result)
	}
	before := waitComplete(t, c)
	node := before.Routes[0]
	request := &paxv1.SaveRouteGroupsRequest{ExpectedCatalogRevision: before.CatalogRevision, ClientId: "client", RequestId: "save", Groups: []*paxv1.RouteGroup{{Id: "work", Name: "工作", MemberIds: []string{node.Id}}, {Id: "media", Name: "影音", MemberIds: []string{node.Id}}}}
	ack, err = c.SaveRouteGroups(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); !result.Ok {
		t.Fatal(result)
	}
	after := waitComplete(t, c)
	if len(after.RouteGroups) != 2 || after.RouteGroups[0].Id != "work" || after.RouteGroups[1].MemberIds[0] != node.Id || after.Routes[0].ConfigRevision != node.ConfigRevision {
		t.Fatal("groups or unchanged route version lost")
	}
	replay, err := c.SaveRouteGroups(ctx, request)
	if err != nil || replay.OperationId != ack.OperationId {
		t.Fatal("request not idempotent", err)
	}
	awaitResult(t, results, replay.OperationId)
	request.RequestId = "stale"
	ack, err = c.SaveRouteGroups(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); result.Ok || result.Error.GetCode() != "catalog_stale" {
		t.Fatal("stale save accepted", result)
	}
	if waitComplete(t, c).CatalogRevision != after.CatalogRevision {
		t.Fatal("failed save mutated catalog")
	}
	request.RequestId = "bad-member"
	request.ExpectedCatalogRevision = after.CatalogRevision
	request.Groups[0].MemberIds = []string{"missing"}
	ack, err = c.SaveRouteGroups(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); result.Ok {
		t.Fatal("missing member silently discarded")
	}
	if waitComplete(t, c).RouteGroups[0].MemberIds[0] != node.Id {
		t.Fatal("invalid save changed members")
	}
}

func TestGroupValidationRejectsWholeDraft(t *testing.T) {
	c, _ := newSubscriptionController(t, &fakeFetcher{})
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	// Query fences catalog initialization through the same owner.
	if _, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: "trojan://fixture@example.test:443#Fixture"}); err != nil {
		t.Fatal(err)
	}
	snapshot := waitComplete(t, c)
	tooMany := make([]*paxv1.RouteGroup, 257)
	for i := range tooMany {
		tooMany[i] = &paxv1.RouteGroup{Id: fmt.Sprint(i), Name: fmt.Sprint(i)}
	}
	for _, groups := range [][]*paxv1.RouteGroup{
		{{Id: "subscription:source", Name: "不可改"}}, {{Id: "all", Name: "不可改"}},
		{{Id: "a", Name: "全部"}}, {{Id: "a", Name: strings.Repeat("字", 25)}},
		{{Id: "a", Name: "同名"}, {Id: "b", Name: "同名"}},
		{{Id: "a", Name: "一"}, {Id: "a", Name: "二"}}, tooMany,
	} {
		ack, err := c.SaveRouteGroups(ctx, &paxv1.SaveRouteGroupsRequest{ExpectedCatalogRevision: snapshot.CatalogRevision, Groups: groups})
		if err != nil {
			t.Fatal(err)
		}
		if result := awaitResult(t, results, ack.OperationId); result.Ok {
			t.Fatal("invalid groups accepted")
		}
		if after := waitComplete(t, c); after.CatalogRevision != snapshot.CatalogRevision || len(after.RouteGroups) != 0 {
			t.Fatal("invalid groups changed catalog")
		}
	}
}

func TestGroupsSurviveRestartAndDeleteOnlyMembership(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	repository := config.NewCatalogStore(filepath.Join(dir, "routes.json"), uint32(os.Getuid()))
	start := func() *Controller {
		c := New("groups-test", "")
		c.ConfigureCatalog(repository)
		c.ConfigureOperations(context.Background(), func() (config.DebugProfile, error) { return config.DebugProfile{}, config.ErrProfileUnavailable })
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := c.CloseProxy(ctx); err != nil {
				t.Error(err)
			}
		})
		return c
	}
	ctx := context.Background()
	c := start()
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.AddRoute(ctx, &paxv1.RouteEditRequest{DocumentJson: `{"type":"trojan","tag":"Durable","server":"example.test","server_port":443,"password":"fixture","tls":{"enabled":true,"server_name":"example.test"}}`})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); !result.Ok {
		t.Fatal(result)
	}
	before := waitComplete(t, c)
	node := before.Routes[0]
	ack, err = c.SaveRouteGroups(ctx, &paxv1.SaveRouteGroupsRequest{ExpectedCatalogRevision: before.CatalogRevision, Groups: []*paxv1.RouteGroup{{Id: "stable", Name: "保存组", MemberIds: []string{node.Id}}}})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); !result.Ok {
		t.Fatal(result)
	}
	if err := c.CloseProxy(ctx); err != nil {
		t.Fatal(err)
	}
	restarted := start()
	restartResults, stopRestart := operationSink(restarted)
	defer stopRestart()
	if _, err := restarted.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: node.Id}); err != nil {
		t.Fatal(err)
	}
	after := waitComplete(t, restarted)
	if len(after.RouteGroups) != 1 || after.RouteGroups[0].Id != "stable" || after.RouteGroups[0].MemberIds[0] != node.Id {
		t.Fatal("restart lost groups")
	}
	ack, err = restarted.DeleteRoute(ctx, &paxv1.DeleteRouteRequest{RouteId: node.Id, ExpectedRevision: node.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, restartResults, ack.OperationId); !result.Ok {
		t.Fatal(result)
	}
	after = waitComplete(t, restarted)
	if len(after.RouteGroups) != 1 || len(after.RouteGroups[0].MemberIds) != 0 || len(after.Routes) != 0 {
		t.Fatal("delete did not clean membership")
	}
	ack, err = restarted.SaveRouteGroups(ctx, &paxv1.SaveRouteGroupsRequest{ExpectedCatalogRevision: after.CatalogRevision})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, restartResults, ack.OperationId); !result.Ok {
		t.Fatal(result)
	}
	if len(waitComplete(t, restarted).RouteGroups) != 0 {
		t.Fatal("empty draft did not delete groups")
	}
}

type rejectingGroupStore struct {
	memoryCatalog
	reject bool
}

func (s *rejectingGroupStore) Save(ctx context.Context, c config.Catalog) error {
	if s.reject {
		return config.ErrCatalogWrite
	}
	return s.memoryCatalog.Save(ctx, c)
}
func TestGroupWriteFailureLeavesPreviousGroups(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := New("test", "")
	repository := &rejectingGroupStore{memoryCatalog: memoryCatalog{catalog: config.NewCatalog(), exists: true}}
	c.ConfigureCatalog(repository)
	c.ConfigureOperations(ctx, func() (config.DebugProfile, error) { return config.DebugProfile{}, config.ErrProfileUnavailable })
	results, stop := operationSink(c)
	defer stop()
	if _, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: "trojan://fixture@example.test:443#Fixture"}); err != nil {
		t.Fatal(err)
	}
	before := waitComplete(t, c)
	repository.reject = true
	ack, err := c.SaveRouteGroups(ctx, &paxv1.SaveRouteGroupsRequest{ExpectedCatalogRevision: before.CatalogRevision, Groups: []*paxv1.RouteGroup{{Id: "g", Name: "原子写入"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); result.Ok || result.Error.GetCode() != "route_save_failed" {
		t.Fatal("write failure reported success")
	}
	after := waitComplete(t, c)
	if len(after.RouteGroups) != 0 || after.CatalogRevision != before.CatalogRevision {
		t.Fatal("write failure changed snapshot")
	}
}

func TestGroupMembershipBudgetRejectsWholeTransaction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	catalog := config.NewCatalog()
	ids := make([]string, 1000)
	for i := range ids {
		ids[i] = fmt.Sprintf("route-%d", i)
		route, err := config.RouteFromDocument([]byte(`{"type":"trojan","tag":"Fixture","server":"example.test","server_port":443,"password":"fixture","tls":{"enabled":true,"server_name":"example.test"}}`), ids[i], "Fixture")
		if err != nil {
			t.Fatal(err)
		}
		catalog.Routes = append(catalog.Routes, config.RouteRecord{Config: route, Revision: 1, ConfigRevision: 1})
	}
	c := New("test", "")
	c.ConfigureCatalog(&memoryCatalog{catalog: catalog, exists: true})
	c.ConfigureOperations(ctx, func() (config.DebugProfile, error) { return config.DebugProfile{}, config.ErrProfileUnavailable })
	results, stop := operationSink(c)
	defer stop()
	if _, err := c.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: ids[0]}); err != nil {
		t.Fatal(err)
	}
	before := waitComplete(t, c)
	groups := make([]*paxv1.RouteGroup, 21)
	for i := range groups {
		groups[i] = &paxv1.RouteGroup{Id: fmt.Sprint(i), Name: fmt.Sprint(i), MemberIds: ids}
	}
	ack, err := c.SaveRouteGroups(ctx, &paxv1.SaveRouteGroupsRequest{ExpectedCatalogRevision: before.CatalogRevision, Groups: groups})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); result.Ok || result.Error.GetCode() != "group_limit" {
		t.Fatal("membership budget not enforced", result)
	}
	if waitComplete(t, c).CatalogRevision != before.CatalogRevision {
		t.Fatal("budget failure wrote")
	}
	ack, err = c.SaveRouteGroups(ctx, &paxv1.SaveRouteGroupsRequest{ExpectedCatalogRevision: before.CatalogRevision, Groups: groups[:20]})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); !result.Ok {
		t.Fatal("exact budget rejected", result)
	}
	if len(waitComplete(t, c).RouteGroups) != 20 {
		t.Fatal("groups truncated")
	}
}
