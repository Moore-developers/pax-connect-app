package controller

import (
	"context"
	"os"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
)

func TestClaudeUserMessageAutomaticallyScansAndPushesPreview(t *testing.T) {
	r := newAIUsageRig(t, true)
	ctx := context.Background()
	if err := os.WriteFile(r.log, []byte(`{"type":"last-prompt","sessionId":"empty"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.c.SetViewVisibility(ctx, &pb.ViewVisibility{Views: []string{"ai_sessions"}}); err != nil {
		t.Fatal(err)
	}
	server := NewAIUsageServer(r.store, nil)
	deadline := time.After(10 * time.Second)
initial:
	for {
		select {
		case ev := <-r.queue:
			if ev.GetAiUsage() == nil {
				continue
			}
			page, err := server.ListAISessions(ctx, &pb.AISessionCatalogQuery{Cli: "claude"})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) == 1 && page.Items[0].Title == "" {
				break initial
			}
		case <-deadline:
			t.Fatal("automatic initial scan did not discover untitled session")
		}
	}

	start := time.Now()
	f, err := os.OpenFile(r.log, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.WriteString(`{"type":"user","sessionId":"empty","message":{"content":"Automatically recognized new question"}}` + "\n")
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal(writeErr, closeErr)
	}
	// No explicit Scan, query lease, wake, or page reopening after the append.
	// The normal visible cadence must discover the message and publish Watch.
	deadline = time.After(10 * time.Second)
	for {
		select {
		case ev := <-r.queue:
			if ev.GetAiUsage() == nil {
				continue
			}
			page, err := server.ListAISessions(ctx, &pb.AISessionCatalogQuery{Query: "Automatically recognized"})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) == 1 && page.Items[0].TitleSource == "claude.user-message" {
				if ev.GetAiUsage().Revision != 0 || ev.GetAiUsage().Generation != 0 {
					t.Fatal("preview advanced accounting", ev.GetAiUsage())
				}
				t.Logf("new user message automatically scanned and pushed in %s", time.Since(start))
				return
			}
		case <-deadline:
			t.Fatal("new user message did not automatically become a pushed preview")
		}
	}
}
