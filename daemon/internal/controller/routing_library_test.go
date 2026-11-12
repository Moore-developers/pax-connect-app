package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

type memoryRouting struct {
	value config.RoutingLibrary
	fail  bool
}

func (m *memoryRouting) Load() (config.RoutingLibrary, bool, error) {
	return m.value.Clone(), true, nil
}
func (m *memoryRouting) Save(_ context.Context, l config.RoutingLibrary) error {
	if m.fail {
		return errors.New("disk_failed")
	}
	m.value = l.Clone()
	return nil
}
func routingController(t *testing.T) (*Controller, *memoryRouting) {
	t.Helper()
	c := New("fixture", "")
	c.ConfigureCatalog(&memoryCatalog{catalog: config.NewCatalog(), exists: true})
	repo := &memoryRouting{value: config.NewRoutingLibrary()}
	c.ConfigureRoutingLibrary(repo, func(raw []byte) ([]config.RoutingEntry, string, error) {
		if string(raw) == "bad" {
			return nil, "", config.ErrRoutingLibrary
		}
		return []config.RoutingEntry{{ID: string(raw), Method: "source", Target: "域名：example.com", Destination: "global", Enabled: true, Match: []byte(`{"domain":["example.com"]}`)}}, string(raw), nil
	})
	c.routing.applicationQuery = func(context.Context) []config.RoutingApplication {
		return []config.RoutingApplication{{ID: "fixture.app", Name: "测试应用", Path: "/Applications/Fixture.app", Bundle: true, Domains: []string{"example.com"}}}
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.ConfigureOperations(ctx, func() (config.DebugProfile, error) { return config.DebugProfile{}, config.ErrProfileUnavailable })
	t.Cleanup(func() {
		cancel()
		done, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := c.CloseProxy(done); err != nil {
			t.Error(err)
		}
	})
	return c, repo
}
func saveRoutingPackage(t *testing.T, c *Controller, revision uint64, id, destination string) *pb.RoutingLibrarySnapshot {
	t.Helper()
	v, e := c.MutateRoutingLibrary(context.Background(), &pb.RoutingMutationRequest{ExpectedRevision: revision, Action: "save_package", Package: &pb.RoutingPackage{Id: id, Name: id, Kind: "local", Destination: destination}})
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestRoutingManagementOffCommandsAndCAS(t *testing.T) {
	c, repo := routingController(t)
	ctx := context.Background()
	initial, e := c.QueryRoutingLibrary(ctx, &emptypb.Empty{})
	if e != nil || initial.State != "configured" || len(initial.Packages) != 2 || len(initial.Applications) != 1 {
		t.Fatal(initial, e)
	}
	v := saveRoutingPackage(t, c, initial.Revision, "mine", "direct")
	if len(v.Packages) != 3 {
		t.Fatal(v)
	}
	v, e = c.MutateRoutingLibrary(ctx, &pb.RoutingMutationRequest{ExpectedRevision: v.Revision, Action: "save_rule", PackageId: "mine", Rule: &pb.RoutingEntry{Id: "one", Method: "domain", Target: "EXAMPLE.COM.", Destination: "direct", Enabled: true}})
	if e != nil || v.Packages[1].Rules[0].Target != "example.com" {
		t.Fatal(v, e)
	}
	if _, e = c.MutateRoutingLibrary(ctx, &pb.RoutingMutationRequest{ExpectedRevision: 1, Action: "delete_package", PackageId: "mine"}); status.Code(e) != codes.Aborted {
		t.Fatal("stale request overwrote state", e)
	}
	v, e = c.MutateRoutingLibrary(ctx, &pb.RoutingMutationRequest{ExpectedRevision: v.Revision, Action: "enable_package", PackageId: "mine", Enabled: false})
	if e != nil || v.Packages[1].Enabled {
		t.Fatal(v, e)
	}
	repo.fail = true
	if _, e = c.MutateRoutingLibrary(ctx, &pb.RoutingMutationRequest{ExpectedRevision: v.Revision, Action: "delete_package", PackageId: "mine"}); e == nil {
		t.Fatal("write failure hidden")
	}
	repo.fail = false
	latest, e := c.QueryRoutingLibrary(ctx, &emptypb.Empty{})
	if e != nil || latest.Revision != v.Revision || len(latest.Packages) != 3 {
		t.Fatal(latest, e)
	}
	if c.coreSession != nil {
		t.Fatal("OFF editing required a core")
	}
}
func TestRoutingSubscriptionFailureKeepsVersionAndRollbackPersists(t *testing.T) {
	c, repo := routingController(t)
	f := &fakeFetcher{}
	f.set("v1", nil)
	c.ConfigureSubscriptions(f)
	ctx := context.Background()
	v, e := c.MutateRoutingLibrary(ctx, &pb.RoutingMutationRequest{ExpectedRevision: 1, Action: "save_package", Package: &pb.RoutingPackage{Id: "sub", Name: "服务订阅", Kind: "subscription", Source: "https://example.com/rules.json", Destination: "global"}})
	if e != nil || v.Packages[1].Version != "v1" {
		t.Fatal(v, e)
	}
	f.set("v2", nil)
	v, e = c.MutateRoutingLibrary(ctx, &pb.RoutingMutationRequest{ExpectedRevision: v.Revision, Action: "update", PackageId: "sub"})
	if e != nil || v.Packages[1].PreviousVersion != "v1" {
		t.Fatal(v, e)
	}
	f.set("bad", nil)
	if _, e = c.MutateRoutingLibrary(ctx, &pb.RoutingMutationRequest{ExpectedRevision: v.Revision, Action: "update", PackageId: "sub"}); e == nil {
		t.Fatal("invalid source accepted")
	}
	if repo.value.Packages[1].Version != "v2" {
		t.Fatal("failed update lost valid rules")
	}
	v, e = c.MutateRoutingLibrary(ctx, &pb.RoutingMutationRequest{ExpectedRevision: v.Revision, Action: "rollback", PackageId: "sub"})
	if e != nil || v.Packages[1].Version != "v1" || v.Packages[1].Automatic || repo.value.Packages[1].Version != "v1" {
		t.Fatal(v, e)
	}
}

type hotRoutingCore struct {
	fakeCoreSession
	updates []uint64
	fail    bool
}

func (f *hotRoutingCore) UpdateRouting(_ context.Context, l config.RoutingLibrary, _ []config.RoutingApplication, _ map[string]string) error {
	f.updates = append(f.updates, l.Revision)
	if f.fail {
		return errors.New("routing_update_failed")
	}
	return nil
}
func TestRoutingActiveUsesHotUpdateAndRecoversUncertainFailure(t *testing.T) {
	c, repo := routingController(t)
	core := &hotRoutingCore{}
	c.ConfigureCoreSession(core)
	c.proxyState(pb.ProtectionStatus_PROTECTION_STATUS_PROTECTED, "", nil)
	c.mu.Lock()
	c.current.CurrentPathCheck = &pb.CurrentPathCheck{State: "passed"}
	c.mu.Unlock()
	v := saveRoutingPackage(t, c, 1, "mine", "direct")
	if len(core.updates) != 1 || len(core.profiles) != 0 || core.clears != 0 || v.State != "active" {
		t.Fatal("rules used lifecycle instead of hot update", core)
	}
	snapshot, _ := c.GetSnapshot(context.Background(), &pb.GetSnapshotRequest{})
	if snapshot.GetCurrentPathCheck().GetState() != "stale" {
		t.Fatal("rules retained old channel evidence")
	}
	core.fail = true
	if _, err := c.MutateRoutingLibrary(context.Background(), &pb.RoutingMutationRequest{ExpectedRevision: v.Revision, Action: "delete_package", PackageId: "mine"}); err == nil {
		t.Fatal("unconfirmed runtime accepted")
	}
	if len(core.updates) != 3 || repo.value.Revision != v.Revision || c.routing.loadError == nil {
		t.Fatal("failed recovery was not fenced")
	}
}

func TestRoutingSubscriptionRenameDoesNotFetchOrLoseRollback(t *testing.T) {
	c, _ := routingController(t)
	f := &fakeFetcher{}
	f.set("v1", nil)
	c.ConfigureSubscriptions(f)
	ctx := context.Background()
	q := &pb.RoutingMutationRequest{ExpectedRevision: 1, Action: "save_package", Package: &pb.RoutingPackage{Id: "sub", Name: "old", Kind: "subscription", Source: "https://example.com/rules.json", Destination: "global"}}
	v, err := c.MutateRoutingLibrary(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	f.set("v2", nil)
	v, err = c.MutateRoutingLibrary(ctx, &pb.RoutingMutationRequest{ExpectedRevision: v.Revision, Action: "update", PackageId: "sub"})
	if err != nil {
		t.Fatal(err)
	}
	f.set("", errors.New("offline"))
	q.ExpectedRevision = v.Revision
	q.Package.Name = "renamed"
	v, err = c.MutateRoutingLibrary(ctx, q)
	if err != nil || v.Packages[1].PreviousVersion != "v1" || v.Packages[1].Version != "v2" || v.Packages[1].Name != "renamed" {
		t.Fatal(v, err)
	}
}
