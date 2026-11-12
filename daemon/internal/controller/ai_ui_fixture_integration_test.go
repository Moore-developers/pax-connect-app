//go:build integration

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/aiprofile"
	"pax-connect/daemon/internal/aiusage"
)

// configureAINativeUIFixture adds only isolated AI services to the opt-in history socket.
// Its tick handles append-ai / expire-ai inside root and writes action.done with the
// authoritative revision, generation and request count. It never changes HOME or wall time.
func configureAINativeUIFixture(t *testing.T, c *Controller, ctx context.Context, root string, server *grpc.Server) func() {
	t.Helper()
	c.ConfigureUserMode(func() {}, 0)
	home := filepath.Join(root, "ai-home")
	logDir := filepath.Join(home, ".claude", "projects", "fixture")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		t.Fatal(err)
	}
	profiles, err := aiprofile.New(aiprofile.Options{Directory: filepath.Join(home, "profiles"), Home: home, Owner: uint32(os.Geteuid())})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = profiles.Close() })
	const profileID = "4eb87612-9a70-49d3-88e0-a54d40245269"
	profileServer := NewAIProfilesServer(profiles)
	if _, err = profileServer.MutateAIProfile(ctx, &pb.AIProfileMutation{OperationId: "fixture-save", Action: &pb.AIProfileMutation_Save{Save: &pb.AIProfileSave{Profile: &pb.AIProfileSummary{Id: profileID, Name: "隔离用量测试", BaseUrl: "https://fixture.invalid", Multiplier: 2}, ReplaceKey: true, ApiKey: "fixture-not-a-real-secret"}}}); err != nil {
		t.Fatal(err)
	}
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	store, err := aiusage.Open(ctx, aiusage.Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid()), Now: func() time.Time { return time.Unix(0, clock.Load()) }, Timeline: func(context.Context) (aiusage.Resolver, error) {
		return func(string, time.Time) aiusage.Attribution {
			return aiusage.Attribution{ProfileID: profileID, ProfileName: "隔离用量测试", Multiplier: 1}
		}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	next := 0
	appendRows := func(count int) {
		t.Helper()
		file, err := os.OpenFile(filepath.Join(logDir, "session.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < count; i++ {
			next++
			at := time.Now().Add(-time.Duration(100-next) * time.Second).UTC().Format(time.RFC3339Nano)
			line := fmt.Sprintf(`{"type":"assistant","sessionId":"fixture","requestId":"r-%d","timestamp":"%s","message":{"id":"m-%d","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":1000000,"output_tokens":1000000}}}`+"\n", next, at, next)
			if _, err := file.WriteString(line); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if err := store.Scan(ctx); err != nil {
			t.Fatal(err)
		}
	}
	appendRows(25)
	if err := store.Configure(ctx); err != nil {
		t.Fatal(err)
	}
	c.ConfigureAIProfiles()
	c.ConfigureAIUsage(ctx, store)
	usageServer := NewAIUsageServer(store, profiles)
	readyNow := time.Now()
	readyLogs, err := usageServer.ListAIUsageLogs(ctx, &pb.AIUsageLogQuery{FromMs: readyNow.Add(-24 * time.Hour).UnixMilli(), ToMs: readyNow.Add(time.Hour).UnixMilli()})
	if err != nil || readyLogs.GetAll() < 25 || len(readyLogs.GetRequests()) == 0 {
		t.Fatalf("fixture AI logs not readable: %v %v", readyLogs, err)
	}
	pb.RegisterAIProfilesServiceServer(server, profileServer)
	pb.RegisterAIUsageServiceServer(server, usageServer)
	return func() {
		for _, action := range []string{"append-ai", "expire-ai"} {
			path := filepath.Join(root, action)
			if _, err := os.Stat(path); os.IsNotExist(err) {
				continue
			} else if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if action == "append-ai" {
				appendRows(2)
			} else {
				clock.Store(time.Now().Add(94 * 24 * time.Hour).UnixNano())
				if err := store.Scan(ctx); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now()
			page, err := usageServer.ListAIUsageLogs(ctx, &pb.AIUsageLogQuery{FromMs: now.Add(-24 * time.Hour).UnixMilli(), ToMs: now.Add(time.Hour).UnixMilli()})
			if err != nil {
				t.Fatal(err)
			}
			state := store.State()
			marker, err := json.Marshal(map[string]any{"revision": state.Revision, "generation": state.Generation, "requests": page.All, "profileID": profileID})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path+".done", marker, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
