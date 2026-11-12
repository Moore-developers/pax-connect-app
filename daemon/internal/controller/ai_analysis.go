package controller

import (
	"context"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/aiusage"
	"time"
)

// QueryAIUsageAnalysis returns one consistent retained metadata snapshot.
func (s *AIUsageServer) QueryAIUsageAnalysis(ctx context.Context, q *pb.AIUsageAnalysisQuery) (*pb.AIUsageAnalysis, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	keys := make([]aiusage.SessionKey, len(q.Sessions))
	for i, k := range q.Sessions {
		keys[i] = aiusage.SessionKey{CLI: k.Cli, Session: k.SessionId}
	}

	var a aiusage.Analysis
	var e error
	var comparison []aiusage.LimitComparisonRange
	if (q.ComparisonFromMs == nil) != (q.ComparisonToMs == nil) || (!q.LimitsOnly && q.ComparisonFromMs != nil) {
		return nil, usageError(aiusage.ErrRange)
	}
	if q.ComparisonFromMs != nil {
		comparison = append(comparison, aiusage.LimitComparisonRange{From: *q.ComparisonFromMs, To: *q.ComparisonToMs})
	}
	if q.LimitsOnly {
		a, e = s.store.AnalyzeLimits(ctx, q.FromMs, q.ToMs, keys, comparison...)
	} else {
		a, e = s.store.Analyze(ctx, q.FromMs, q.ToMs, keys)
	}
	if e != nil {
		return nil, usageError(e)
	}
	out := &pb.AIUsageAnalysis{State: usageState(a.State), Truncated: a.Truncated, RetainedFromMs: a.RetainedFrom, Limits: sessionLimits(a.Limits)}
	for _, r := range a.Rows {
		out.Rows = append(out.Rows, &pb.AIUsageAnalysisRow{Cli: r.CLI, SessionId: r.Session, SessionTitle: r.SessionTitle, TurnId: r.Turn, ModelId: r.Model, ReasoningEffort: r.Effort, AtMs: r.At, EndMs: r.Ended, Complete: r.Complete, Compressed: r.Compressed, Totals: usageTotals(r.Summary), MaxInput: r.MaxInput, Turns: r.Turns, KnownTurnRequests: r.KnownTurnRequests, CompletedTurnIds: r.CompletedTurnIDs, PricedRequests: r.PricedRequests})
	}
	for _, r := range a.Requests {
		out.Requests = append(out.Requests, &pb.AIUsageAnalysisRow{Cli: r.CLI, RequestId: r.RequestID, SessionId: r.Session, SessionTitle: r.SessionTitle, TurnId: r.Turn, ModelId: r.Model, ReasoningEffort: r.Effort, AtMs: r.At, EndMs: r.Ended, Complete: r.Complete, Compressed: r.Compressed, Totals: usageTotals(r.Summary), MaxInput: r.MaxInput, Turns: r.Turns, KnownTurnRequests: r.KnownTurnRequests, CompletedTurnIds: r.CompletedTurnIDs, PricedRequests: r.PricedRequests})
	}
	for _, r := range a.ContextUsage {
		v := &pb.AIUsageContextUsage{Cli: r.CLI, SessionId: r.Session, ModelId: r.Model, ReasoningEffort: r.Effort, Requests: r.Requests, Turns: r.Turns, KnownTurnRequests: r.KnownTurnRequests, Subagents: r.Subagents, SubagentsIncomplete: r.SubagentsIncomplete}
		for _, b := range r.Bands {
			v.Bands = append(v.Bands, &pb.AIUsageContextBand{Input: b.Input, OfficialCostMicro: b.OfficialCost, ChannelCostMicro: b.ChannelCost, Requests: b.Requests, PricedRequests: b.PricedRequests})
		}
		for _, b := range r.SubagentBands {
			v.SubagentBands = append(v.SubagentBands, &pb.AIUsageContextBand{Input: b.Input, OfficialCostMicro: b.OfficialCost, ChannelCostMicro: b.ChannelCost, Requests: b.Requests, PricedRequests: b.PricedRequests})
		}
		out.ContextUsage = append(out.ContextUsage, v)
	}
	return out, nil
}
