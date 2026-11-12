package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/aiprofile"
	"pax-connect/daemon/internal/aiusage"
)

func TestRepriceAIUsageUsesTheProfilesCurrentMultipliersFromTheStore(t *testing.T) {
	ctx := context.Background()
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	profiles, e := aiprofile.New(aiprofile.Options{Directory: filepath.Join(home, "profiles"), Home: home, Owner: uint32(os.Geteuid())})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = profiles.Close() }()
	const id = "4eb87612-9a70-49d3-88e0-a54d40245269"
	// The multipliers cross the proto boundary and persist: model entry .5, default 2.
	server := NewAIProfilesServer(profiles)
	reply, e := server.MutateAIProfile(ctx, &pb.AIProfileMutation{OperationId: "save", Action: &pb.AIProfileMutation_Save{Save: &pb.AIProfileSave{Profile: &pb.AIProfileSummary{Id: id, Name: "Gateway", BaseUrl: "https://example.test", Multiplier: 2, ModelMultipliers: map[string]float64{"claude-sonnet-4-5-20250929": .5}}, ReplaceKey: true, ApiKey: "k"}}})
	if e != nil || reply.Profiles[0].ModelMultipliers["claude-sonnet-4-5-20250929"] != .5 {
		t.Fatalf("%v %v", reply, e)
	}
	if _, e = server.MutateAIProfile(ctx, &pb.AIProfileMutation{OperationId: "bad", Action: &pb.AIProfileMutation_Save{Save: &pb.AIProfileSave{Profile: &pb.AIProfileSummary{Id: id, Name: "Gateway", BaseUrl: "https://example.test", Multiplier: 0.1234}, ExpectedRevision: 1}}}); status.Code(e) != codes.FailedPrecondition {
		t.Fatalf("four decimals: %v", e)
	}

	logs := filepath.Join(home, ".claude", "projects", "p")
	if e = os.MkdirAll(logs, 0700); e != nil {
		t.Fatal(e)
	}
	at := time.Date(2026, 10, 27, 10, 0, 0, 0, time.Local)
	line := `{"type":"assistant","sessionId":"s","requestId":"r-1","timestamp":"` + at.UTC().Format(time.RFC3339) + `","message":{"id":"m1","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":1000000,"output_tokens":1000000}}}` + "\n"
	if e = os.WriteFile(filepath.Join(logs, "a.jsonl"), []byte(line), 0600); e != nil {
		t.Fatal(e)
	}
	now := at.Add(24 * time.Hour)
	store, e := aiusage.Open(ctx, aiusage.Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid()), Now: func() time.Time { return now },
		Timeline: func(context.Context) (aiusage.Resolver, error) {
			return func(string, time.Time) aiusage.Attribution {
				return aiusage.Attribution{ProfileID: id, ProfileName: "Gateway", Multiplier: 1}
			}, nil
		}})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = store.Close(ctx) }()
	if e = store.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	usage := NewAIUsageServer(store, profiles)
	day := at.Format("2006-01-02")
	// Sonnet 4.5 at 1x is 18 USD; the profile now charges its model entry .5 = 9 USD.
	dry, e := usage.RepriceAIUsage(ctx, &pb.RepriceAIUsageRequest{ProfileId: id, FromDay: day, ToDay: day, DryRun: true})
	if e != nil || !dry.DryRun || dry.Requests != 1 || dry.OldCostMicro != 18000000 || dry.NewCostMicro != 9000000 || dry.OverrideId != 0 {
		t.Fatalf("%v %v", dry, e)
	}
	done, e := usage.RepriceAIUsage(ctx, &pb.RepriceAIUsageRequest{ProfileId: id, All: true})
	if e != nil || done.NewCostMicro != 9000000 || done.OverrideId != 1 || done.State.Revision <= dry.State.Revision {
		t.Fatalf("%v %v", done, e)
	}
	model := "claude-opus-5-5"
	scoped, e := usage.RepriceAIUsage(ctx, &pb.RepriceAIUsageRequest{ProfileId: id, ModelId: &model, All: true, DryRun: true})
	if e != nil || scoped.Requests != 0 {
		t.Fatalf("model scope: %v %v", scoped, e)
	}
	empty := ""
	for name, r := range map[string]*pb.RepriceAIUsageRequest{
		"no profile":   {All: true},
		"no range":     {ProfileId: id},
		"bad day":      {ProfileId: id, FromDay: "nope", ToDay: "nope"},
		"blank model":  {ProfileId: id, ModelId: &empty, All: true},
		"both ranges":  {ProfileId: id, All: true, FromDay: day, ToDay: day},
		"reversed day": {ProfileId: id, FromDay: "2026-10-28", ToDay: "2026-10-27"},
	} {
		if _, e = usage.RepriceAIUsage(ctx, r); status.Code(e) != codes.InvalidArgument {
			t.Fatalf("%s: %v", name, e)
		}
	}
	if _, e = usage.RepriceAIUsage(ctx, &pb.RepriceAIUsageRequest{ProfileId: "00000000-0000-0000-0000-000000000000", All: true}); status.Code(e) != codes.NotFound {
		t.Fatalf("unknown profile: %v", e)
	}
	if _, e = NewAIUsageServer(store, nil).RepriceAIUsage(ctx, &pb.RepriceAIUsageRequest{ProfileId: id, All: true}); status.Code(e) != codes.Unavailable {
		t.Fatalf("no profile store: %v", e)
	}
}
