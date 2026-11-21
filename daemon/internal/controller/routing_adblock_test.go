package controller

import (
	"context"
	"slices"
	"testing"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

func TestClaudePackageIsDisabledOnUpgradeAndUsesExistingPackageCommands(t *testing.T) {
	ctx := context.Background()
	c := New("fixture", "")
	repo := &memoryRouting{value: config.NewRoutingLibrary()}
	c.ConfigureRoutingLibrary(repo, nil)
	if err := c.ConfigureClaudeRules(ctx); err != nil {
		t.Fatal(err)
	}
	if got := c.routingLibrarySnapshot("").Packages[1]; got.Id != config.ClaudePackageID || got.Enabled || len(got.Rules) == 0 {
		t.Fatal(got)
	}
	library := repo.value.Clone()
	for _, q := range []*pb.RoutingMutationRequest{
		{Action: "save_package", Package: &pb.RoutingPackage{Id: "mine", Kind: "local", Name: "Claude", Destination: "direct"}},
		{Action: "destination", PackageId: config.ClaudePackageID, Destination: "route:fixed"},
		{Action: "enable_package", PackageId: config.ClaudePackageID, Enabled: true},
	} {
		if err := mutateRouting(&library, q, nil, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	if library.Packages[1].ID != "mine" || library.Validate() != nil {
		t.Fatal("custom package must precede the builtin")
	}
	for _, action := range []string{"delete_package", "replace_rules", "save_rule", "update"} {
		if err := mutateRouting(&library, &pb.RoutingMutationRequest{Action: action, PackageId: config.ClaudePackageID}, nil, "", nil); err == nil {
			t.Fatal("read-only package accepted", action)
		}
	}
	if err := mutateRouting(&library, &pb.RoutingMutationRequest{Action: "move", PackageId: "mine", Delta: 1}, nil, "", nil); err == nil {
		t.Fatal("custom package moved across builtin")
	}
	library.Revision++
	repo.value = library
	c.ConfigureRoutingLibrary(repo, nil)
	if err := c.ConfigureClaudeRules(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot := c.routingLibrarySnapshot("")
	i := slices.IndexFunc(snapshot.Packages, func(p *pb.RoutingPackage) bool { return p.Id == config.ClaudePackageID })
	if i != 2 || snapshot.Revision != library.Revision || !snapshot.Packages[i].Enabled || snapshot.Packages[i].Destination != "route:fixed" {
		t.Fatal(snapshot)
	}
}

func TestAdblockBaselinePersistsOnceAndKeepsUserChoices(t *testing.T) {
	c := New("fixture", "")
	repo := &memoryRouting{value: config.NewRoutingLibrary()}
	c.ConfigureRoutingLibrary(repo, func([]byte) ([]config.RoutingEntry, string, error) {
		return []config.RoutingEntry{{ID: "ads", Method: "source", Target: "广告域名", Destination: "global", Enabled: true, Match: []byte(`{"domain_suffix":["doubleclick.net"]}`)}}, "baseline", nil
	})
	ctx := context.Background()
	repo.fail = true
	if c.ConfigureAdblockRules(ctx, "../../../rulesets") == nil || len(c.routing.library.Packages) != 2 {
		t.Fatal("failed save changed live library")
	}
	repo.fail = false
	if err := c.ConfigureAdblockRules(ctx, "../../../rulesets"); err != nil {
		t.Fatal(err)
	}
	l := repo.value
	if len(l.Packages) != 3 || l.Packages[1].Enabled || l.Packages[1].Destination != "block" {
		t.Fatal(l)
	}
	l.Packages[1].Enabled = true
	l.Packages[1].Automatic = false
	l.Packages[1].Version = "user-rollback"
	repo.value = l
	c.ConfigureRoutingLibrary(repo, c.routing.decode)
	if err := c.ConfigureAdblockRules(ctx, "missing-assets"); err != nil {
		t.Fatal(err)
	}
	if c.routing.library.Revision != l.Revision || !c.routing.library.Packages[1].Enabled || c.routing.library.Packages[1].Automatic || c.routing.library.Packages[1].Version != "user-rollback" {
		t.Fatal("upgrade overwrote choice")
	}
}

func TestAdblockManagementOrderAndRollback(t *testing.T) {
	l := config.NewRoutingLibrary()
	ads := config.RoutingPackage{ID: config.AdblockPackageID, Name: "广告拦截", Kind: "adblock", Destination: "block", Source: config.AdblockSource, Version: "v1", Rules: []config.RoutingEntry{{ID: "ads", Method: "source", Enabled: true, Destination: "global", Match: []byte(`{"domain_suffix":["doubleclick.net"]}`)}}}
	l.Packages = append(l.Packages[:1], ads, l.Packages[1])
	for _, action := range []string{"delete_package", "destination", "move", "replace_rules", "save_rule"} {
		next := l.Clone()
		if mutateRouting(&next, &pb.RoutingMutationRequest{Action: action, PackageId: config.AdblockPackageID, Delta: -1, Destination: "direct"}, nil, "", nil) == nil {
			t.Fatal("allowed", action)
		}
	}
	if err := mutateRouting(&l, &pb.RoutingMutationRequest{Action: "save_package", Package: &pb.RoutingPackage{Id: "mine", Name: "广告拦截", Kind: "local", Destination: "direct"}}, nil, "", nil); err != nil {
		t.Fatal(err)
	}
	if l.Packages[1].ID != "mine" || l.Packages[2].ID != config.AdblockPackageID || l.Validate() != nil {
		t.Fatal("custom order or duplicate display name", l)
	}
	if mutateRouting(&l, &pb.RoutingMutationRequest{Action: "move", PackageId: "mine", Delta: 1}, nil, "", nil) == nil {
		t.Fatal("custom crossed builtin")
	}
	updated := []config.RoutingEntry{{ID: "new", Method: "source", Enabled: true, Destination: "global", Match: []byte(`{"domain_suffix":["cnzz.com"]}`)}}
	if err := mutateRouting(&l, &pb.RoutingMutationRequest{Action: "update", PackageId: config.AdblockPackageID}, updated, "v2", nil); err != nil {
		t.Fatal(err)
	}
	if err := mutateRouting(&l, &pb.RoutingMutationRequest{Action: "rollback", PackageId: config.AdblockPackageID}, nil, "", nil); err != nil {
		t.Fatal(err)
	}
	if p := l.Packages[2]; p.Version != "v1" || p.Automatic || p.PreviousVersion != "" || p.Rules[0].ID != "ads" {
		t.Fatal(p)
	}
}

func TestAdblockSourceRejectsChangedPredicateKinds(t *testing.T) {
	for _, raw := range []string{`{"process_name":["Safari"]}`, `{"ip_cidr":["0.0.0.0/0"]}`, `{"domain_suffix":[]}`, `{}`} {
		if validAdblockEntries([]config.RoutingEntry{{Match: []byte(raw)}}) {
			t.Fatal("accepted", raw)
		}
	}
}
