package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/aiprofile"
)

type profileTransport func(*http.Request) (*http.Response, error)

func (f profileTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func profileOperationFixture(t *testing.T, transport http.RoundTripper) (*AIProfilesServer, *Controller, string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := aiprofile.New(aiprofile.Options{Home: home, Directory: filepath.Join(home, "state"), Owner: uint32(os.Geteuid()), UsageOnlyDSH: true, ModelClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	c := New("test", "test")
	s := NewAIProfilesServer(m, c)
	t.Cleanup(func() { s.Close(); _ = m.Close() })
	return s, c, home
}
func profileRequest(c *Controller, n int) *pb.OperationContext {
	return &pb.OperationContext{ClientId: "profile-test", ExpectedDaemonInstance: c.instance, RequestId: fmt.Sprintf("%d:12345678-1234-1234-1234-%012x", time.Now().UnixMilli(), n)}
}
func profileSave(c *Controller) *pb.AIProfileMutation {
	return &pb.AIProfileMutation{Context: profileRequest(c, 1), Action: &pb.AIProfileMutation_Save{Save: &pb.AIProfileSave{Profile: &pb.AIProfileSummary{Id: "4eb87612-9a70-49d3-88e0-a54d40245269", Name: "Saved", BaseUrl: "https://models.test", Multiplier: 1}, ReplaceKey: true, ApiKey: "private-key"}}}
}
func readProfileOperation(t *testing.T, c *Controller, id *pb.OperationContext) *pb.OperationState {
	t.Helper()
	op, err := c.GetOperation(context.Background(), &pb.GetOperationRequest{DaemonInstance: c.instance, Identity: &pb.GetOperationRequest_Request{Request: &pb.OperationRequestIdentity{ClientId: id.ClientId, RequestId: id.RequestId}}})
	if err != nil {
		t.Fatal(err)
	}
	return op
}
func TestProfileSaveResultPrecedesModelsAndSurvivesRetry(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	s, c, _ := profileOperationFixture(t, profileTransport(func(r *http.Request) (*http.Response, error) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader(`{"error":"private-key"}`)), Header: http.Header{}}, nil
	}))
	req := profileSave(c)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	w, err := s.MutateAIProfile(ctx, req)
	if err != nil || len(w.Profiles) != 1 {
		t.Fatalf("save waited for discovery: %v", err)
	}
	<-entered
	op := readProfileOperation(t, c, req.Context)
	if op.State != "succeeded" || op.GetAiProfile().Workspace.Profiles[0].ModelsState != "" {
		t.Fatal(op)
	}
	replay, err := s.MutateAIProfile(ctx, req)
	if err != nil || replay.Profiles[0].Revision != w.Profiles[0].Revision {
		t.Fatalf("replayed: %v", err)
	}
	req.GetSave().Profile.Name = "changed"
	if _, err = s.MutateAIProfile(ctx, req); status.Convert(err).Message() != "request_conflict" {
		t.Fatal(err)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		current, e := s.QueryAIProfiles(context.Background(), &pb.AIProfilesQuery{})
		if e != nil {
			t.Fatal(e)
		}
		if current.Profiles[0].ModelsState == "failed" {
			if current.Profiles[0].ModelsError != "models_auth_failed" {
				t.Fatal(current)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("discovery not persisted")
		}
		time.Sleep(time.Millisecond)
	}
	frozen := readProfileOperation(t, c, req.Context)
	if frozen.GetAiProfile().Workspace.Profiles[0].Revision != w.Profiles[0].Revision {
		t.Fatal("result changed after discovery")
	}
}
func TestProfileModelsCancellationIsOwnedByDaemon(t *testing.T) {
	entered := make(chan struct{}, 2)
	s, c, _ := profileOperationFixture(t, profileTransport(func(r *http.Request) (*http.Response, error) {
		entered <- struct{}{}
		<-r.Context().Done()
		return nil, r.Context().Err()
	}))
	c.proxy = &proxyOwner{} // Real composition includes the network owner.
	req := profileSave(c)
	req.Context = nil
	req.OperationId = "seed"
	// Save-only avoids an implicit provider read in this setup.
	if _, err := s.mutateProfile(context.Background(), req, true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ack, err := s.StartAIProfileModels(ctx, &pb.AIProfileModelsRequest{ProfileId: req.GetSave().Profile.Id, Context: profileRequest(c, 2)})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	<-entered
	query := &pb.GetOperationRequest{DaemonInstance: c.instance, Identity: &pb.GetOperationRequest_OperationId{OperationId: ack.OperationId}}
	op, err := c.GetOperation(context.Background(), query)
	if err != nil || op.State != "running" || !op.Cancellable {
		t.Fatalf("caller cancelled task: %v %v", op, err)
	}
	if _, err = c.CancelOperation(context.Background(), &pb.CancelOperationRequest{OperationId: ack.OperationId}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		op, err = c.GetOperation(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == "cancelled" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(op)
		}
		time.Sleep(time.Millisecond)
	}
}
func TestProfileApplyRechecksExternalFilesAndDSHRejects(t *testing.T) {
	s, c, home := profileOperationFixture(t, nil)
	req := profileSave(c)
	req.Context = nil
	req.OperationId = "seed"
	if _, err := s.mutateProfile(context.Background(), req, true); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".claude")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")
	before := `{"env":{"ANTHROPIC_BASE_URL":"https://old.test","ANTHROPIC_AUTH_TOKEN":"old"},"mcpServers":{"pax":{"command":"pax-mcp"}}}`
	if err := os.WriteFile(path, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := s.QueryAIProfiles(context.Background(), &pb.AIProfilesQuery{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanAIProfileApply(context.Background(), &pb.AIProfilePlanRequest{Cli: "claude", ProfileId: w.Profiles[0].Id, Revision: w.Profiles[0].Revision, InspectionId: w.Bindings[1].InspectionId, Context: profileRequest(c, 3)})
	if err != nil {
		t.Fatal(err)
	}
	external := before + "\n"
	if err = os.WriteFile(path, []byte(external), 0600); err != nil {
		t.Fatal(err)
	}
	apply := &pb.AIProfileApplyRequest{PlanId: plan.Id, Context: profileRequest(c, 4)}
	if _, err = s.ApplyAIProfile(context.Background(), apply); status.Convert(err).Message() != "ai_target_changed" {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != external {
		t.Fatal("external config overwritten")
	}
	if op := readProfileOperation(t, c, apply.Context); op.State != "failed" || op.Error.Code != "ai_target_changed" {
		t.Fatal(op)
	}
	for i, cli := range []string{"dsh", "bad"} {
		if _, err = s.PlanAIProfileApply(context.Background(), &pb.AIProfilePlanRequest{Cli: cli, Context: profileRequest(c, 5+i)}); status.Convert(err).Message() != "ai_template_unsupported" {
			t.Fatal(cli, err)
		}
	}
}

func TestProfileConflictRetainsLatestRevision(t *testing.T) {
	s, c, _ := profileOperationFixture(t, nil)
	req := profileSave(c)
	if _, err := s.MutateAIProfile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	req.Context = profileRequest(c, 8)
	req.GetSave().ExpectedRevision = 99
	req.GetSave().Profile.Name = "Edited"
	_, err := s.MutateAIProfile(context.Background(), req)
	if status.Convert(err).Message() != "ai_profile_conflict" {
		t.Fatal(err)
	}
	op := readProfileOperation(t, c, req.Context)
	if op.State != "failed" || len(op.Error.MessageParams) != 1 || op.Error.MessageParams[0] != "1" {
		t.Fatal(op)
	}
}
