package controller

import (
	"context"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/aiusage"
)

// ListAntigravityModels returns a dated snapshot of the public picker and the
// same versioned reference rates used by local accounting.
func (s *AIUsageServer) ListAntigravityModels(context.Context, *pb.AntigravityModelsQuery) (*pb.AntigravityModels, error) {
	now := time.Now()
	catalog := &pb.AntigravityModels{}
	all := []string{"free_plus", "pro", "ultra", "enterprise"}
	pro := []string{"pro", "ultra", "enterprise"}
	ultra := []string{"ultra", "enterprise"}
	enterprise := []string{"enterprise"}
	const end = "2026-11-30"
	catalog.Models = []*pb.AntigravityModel{
		{Id: "gemini-4-neon", Name: "Gemini 4 Neon", Plans: pro},
		{Id: "gemini-4-carbon", Name: "Gemini 4 Carbon", Plans: ultra},
		{Id: "gemini-4-argon", Name: "Gemini 4 Argon", Plans: pro},
		{Id: "gemini-4-hydrogen", Name: "Gemini 4 Hydrogen", Plans: pro},
		{Id: "gemini-3.8-flash", Name: "Gemini 3.8 Flash", Plans: all},
		{Id: "gemini-3.7-flash", Name: "Gemini 3.7 Flash", Plans: enterprise},
		{Id: "gemini-3.6-flash", Name: "Gemini 3.6 Flash", Plans: enterprise},
		{Id: "gemini-3.1-pro", Name: "Gemini 3.1 Pro", Plans: enterprise},
		{Id: "claude-sonnet-5-5", Name: "Claude Sonnet 5.5 (Thinking)", Plans: pro},
		{Id: "claude-opus-5-5", Name: "Claude Opus 5.5 (Thinking)", Plans: pro},
		{Id: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6 (Thinking)", Plans: all[:2], RetiredOn: end},
		{Id: "claude-opus-4-6", Name: "Claude Opus 4.6 (Thinking)", Plans: all[:2], RetiredOn: end},
		{Id: "gpt-oss-120b", Name: "GPT-OSS 120B", Plans: all[:3], RetiredOn: end},
		{Id: "orchestrator", Name: "Orchestrator", Plans: pro, Kind: "orchestrator"},
		{Id: "nano-banana-2", Name: "Nano Banana 2", Kind: "tool"},
	}
	for _, p := range aiusage.AntigravityPriceSchedule(now) {
		row := &pb.AntigravityRate{
			ModelId: p.Model, Input: p.Input, CacheRead: p.CacheRead,
			CacheWrite: p.CacheWrite, Output: p.Output,
		}
		if !p.From.IsZero() {
			row.EffectiveOn = p.From.Format(time.DateOnly)
		}
		catalog.Rates = append(catalog.Rates, row)
	}
	return catalog, nil
}
