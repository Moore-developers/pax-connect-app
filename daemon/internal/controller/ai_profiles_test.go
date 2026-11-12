package controller

import (
	"context"
	"encoding/json"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"net"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/aiprofile"
	"pax-connect/daemon/internal/ipc"
	"strings"
	"testing"
)

func TestAIProfilesRealUserSocket(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	m, e := aiprofile.New(aiprofile.Options{Directory: filepath.Join(root, "state"), Home: root, Owner: uint32(os.Geteuid())})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = m.Close() }()
	socketDir, e := os.MkdirTemp(os.Getenv("TMPDIR"), "ai-")
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = os.RemoveAll(socketDir) }()
	sock := filepath.Join(socketDir, "ai.sock")
	listener, e := ipc.ListenUser(sock)
	if e != nil {
		t.Fatal(e)
	}
	server := grpc.NewServer()
	pb.RegisterAIProfilesServiceServer(server, NewAIProfilesServer(m))
	defer server.Stop()
	go func() { _ = server.Serve(listener) }()
	connection, e := grpc.NewClient("passthrough:///ai", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}))
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = connection.Close() }()
	client := pb.NewAIProfilesServiceClient(connection)
	reply, e := client.MutateAIProfile(context.Background(), &pb.AIProfileMutation{OperationId: "save", Action: &pb.AIProfileMutation_Save{Save: &pb.AIProfileSave{Profile: &pb.AIProfileSummary{Id: "4eb87612-9a70-49d3-88e0-a54d40245269", Name: "Gateway", BaseUrl: "https://example.test", Multiplier: 1}, ReplaceKey: true, ApiKey: "secret-must-not-return"}}})
	if e != nil {
		t.Fatal(e)
	}
	if len(reply.Profiles) != 1 || !reply.Profiles[0].HasKey {
		t.Fatal("saved metadata missing")
	}
	b, _ := json.Marshal(reply)
	if strings.Contains(string(b), "secret-must-not-return") {
		t.Fatal("RPC returned credential")
	}
	query, e := client.QueryAIProfiles(context.Background(), &pb.AIProfilesQuery{})
	if e != nil || len(query.Profiles) != 1 {
		t.Fatal("query failed")
	}
	cliDir := filepath.Join(root, ".codex")
	if e = os.Mkdir(cliDir, 0700); e != nil {
		t.Fatal(e)
	}
	for n, b := range map[string]string{"config.toml": "model_provider='p'\n[model_providers.p]\nbase_url='https://before.test'\n", "auth.json": `{"OPENAI_API_KEY":"old-key"}`} {
		if e = os.WriteFile(filepath.Join(cliDir, n), []byte(b), 0600); e != nil {
			t.Fatal(e)
		}
	}
	query, e = client.QueryAIProfiles(context.Background(), &pb.AIProfilesQuery{})
	if e != nil {
		t.Fatal(e)
	}
	imported, e := client.MutateAIProfile(context.Background(), &pb.AIProfileMutation{OperationId: "import", Action: &pb.AIProfileMutation_ImportProfile{ImportProfile: &pb.AIProfileImport{Cli: "codex", InspectionId: query.Bindings[0].InspectionId, Name: "Imported", Multiplier: 1}}})
	if e != nil {
		t.Fatal(e)
	}
	plan, e := client.PlanAIProfileApply(context.Background(), &pb.AIProfilePlanRequest{Cli: "codex", ProfileId: reply.Profiles[0].Id, Revision: reply.Profiles[0].Revision, TemplateRevision: imported.Bindings[0].TemplateRevision, InspectionId: imported.Bindings[0].InspectionId})
	if e != nil {
		t.Fatal(e)
	}
	applied, e := client.ApplyAIProfile(context.Background(), &pb.AIProfileApplyRequest{PlanId: plan.Id, OperationId: plan.Id})
	if e != nil {
		t.Fatal(e)
	}
	if applied.Bindings[0].State != "applied" {
		t.Fatal("real socket apply did not commit")
	}
	b, _ = json.Marshal(applied)
	if strings.Contains(string(b), "secret-must-not-return") {
		t.Fatal("apply response leaked key")
	}
	_, e = client.PlanAIProfileApply(context.Background(), &pb.AIProfilePlanRequest{Cli: "codex", ProfileId: reply.Profiles[0].Id})
	if e == nil || !strings.Contains(e.Error(), "ai_target_changed") {
		t.Fatal("unobserved target incorrectly accepted")
	}
}
