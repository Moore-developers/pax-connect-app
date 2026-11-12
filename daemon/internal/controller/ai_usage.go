package controller

import (
	"context"
	"errors"
	"sort"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/aiusage"
)

// AIUsageServer serves local metadata from the independent collector.
type AIUsageServer struct {
	pb.UnimplementedAIUsageServiceServer
	store    *aiusage.Store
	profiles ProfilePricing
}

// ProfilePricing is the Profile store as the repricing RPC sees it: the multipliers a
// Profile charges now. found is false for an unknown Profile.
type ProfilePricing interface {
	Pricing(ctx context.Context, id string) (multiplier float64, models map[string]float64, found bool, err error)
}

// NewAIUsageServer is registered only on the same-UID user socket. profiles may be nil,
// which leaves repricing unavailable.
func NewAIUsageServer(s *aiusage.Store, profiles ProfilePricing) *AIUsageServer {
	return &AIUsageServer{store: s, profiles: profiles}
}
func usageError(e error) error {
	if e == nil {
		return nil
	}
	if errors.Is(e, context.Canceled) {
		return status.Error(codes.Canceled, "ai_usage_cancelled")
	}
	if errors.Is(e, aiusage.ErrBusy) {
		return status.Error(codes.Unavailable, "ai_usage_busy")
	}
	if errors.Is(e, aiusage.ErrRange) {
		return status.Error(codes.InvalidArgument, "ai_usage_range_invalid")
	}
	return status.Error(codes.Unavailable, "ai_usage_unavailable")
}
func usageState(v aiusage.State) *pb.AIUsageState {
	return &pb.AIUsageState{Revision: v.Revision, Backfilling: v.Backfilling, Skipped: v.Skipped, ErrorCode: v.Error, Generation: v.Generation, CatalogRevision: v.CatalogRevision, CatalogReady: v.CatalogReady, CatalogError: v.CatalogError}
}
func usageTotals(v aiusage.Summary) *pb.AIUsageTotals {
	return &pb.AIUsageTotals{Count: v.Count, Success: v.Success, Input: v.Input, Read: v.Read, Write: v.Write, Output: v.Output, CostsMicro: v.Costs[:], CostMicro: v.Cost, TpsSum: v.TPSSum, ElapsedSum: v.ElapsedSum, TpsCount: v.TPSCount, ElapsedCount: v.ElapsedCount, BilledCostsMicro: v.BilledCosts[:]}
}
func usageBuckets(rows []aiusage.Bucket, state aiusage.State) *pb.AIUsageBuckets {
	out := &pb.AIUsageBuckets{State: usageState(state)}
	for _, b := range rows {
		out.Buckets = append(out.Buckets, &pb.AIUsageBucket{AtMs: b.At, Cli: b.CLI, ProfileId: b.ProfileID, ProfileName: b.ProfileName, ModelId: b.Model, Totals: usageTotals(b.Summary)})
	}
	return out
}

// QueryAIUsageBuckets supports metadata-only revision probes for visible pages.
func (s *AIUsageServer) QueryAIUsageBuckets(ctx context.Context, q *pb.AIUsageQuery) (*pb.AIUsageBuckets, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if q.ToMs <= q.FromMs || q.ToMs-q.FromMs > aiusage.MaxQuerySpanMillis || q.Granularity != "hour" && q.Granularity != "day" {
		return nil, usageError(aiusage.ErrRange)
	}
	state := s.store.State()
	if q.KnownRevision != nil && *q.KnownRevision == state.Revision {
		return &pb.AIUsageBuckets{State: usageState(state), NotModified: true}, nil
	}
	b, e := s.store.Buckets(ctx, aiusage.Query{From: q.FromMs, To: q.ToMs, Granularity: q.Granularity})
	out := usageBuckets(b, state)
	if e == nil {
		var last map[string]int64
		if last, e = s.store.LastUsed(ctx); e == nil {
			for cli, at := range last {
				out.LastUsed = append(out.LastUsed, &pb.AIUsageLastUsed{Cli: cli, AtMs: at})
			}
			sort.Slice(out.LastUsed, func(i, j int) bool { return out.LastUsed[i].Cli < out.LastUsed[j].Cli })
		}
	}
	return out, usageError(e)
}

// SummarizeAIUsage uses permanent aggregates for editor lifetime statistics.
func (s *AIUsageServer) SummarizeAIUsage(ctx context.Context, _ *pb.AIUsageSummaryQuery) (*pb.AIUsageBuckets, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state := s.store.State()
	b, e := s.store.Summarize(ctx)
	return usageBuckets(b, state), usageError(e)
}

// ListAIUsageLogs paginates ten metadata rows, with counts independent of result tabs.
func (s *AIUsageServer) ListAIUsageLogs(ctx context.Context, q *pb.AIUsageLogQuery) (*pb.AIUsageLogs, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if q.ToMs <= q.FromMs || q.ToMs-q.FromMs > aiusage.MaxQuerySpanMillis || q.Page > 100000 {
		return nil, usageError(aiusage.ErrRange)
	}
	v, state, e := readAIUsageLogs(ctx, s.store.List, s.store.State, aiusage.Query{From: q.FromMs, To: q.ToMs, CLI: q.Cli, Profile: q.ProfileId, Model: q.ModelId, Text: q.Query, Result: q.Succeeded, Page: int(q.Page), MinOfficialCost: q.MinOfficialCostMicro, MaxOfficialCost: q.MaxOfficialCostMicro})
	out := &pb.AIUsageLogs{All: v.All, Success: v.Success, Failed: v.Failed, Totals: usageTotals(v.Total), State: usageState(state)}
	for _, r := range v.Requests {
		parts := make([]*pb.AIUsageUnitPrice, 4)
		for i, p := range r.Details.Prices {
			parts[i] = &pb.AIUsageUnitPrice{Value: p}
		}
		out.Requests = append(out.Requests, &pb.AIUsageRequest{CacheWriteShortPrice: r.Details.CacheWritePrices[0], CacheWriteLongPrice: r.Details.CacheWritePrices[1], ComponentPrices: parts, PriceVersion: r.Details.PriceVersion, ReasoningEffort: r.Details.ReasoningEffort, EffortSource: r.Details.EffortSource, CacheWriteShort: r.Details.WriteShort, CacheWriteLong: r.Details.WriteLong, Id: r.CLI + ":" + r.Key, AtMs: r.At, Cli: r.CLI, ModelId: r.Model, ProfileId: r.ProfileID, ProfileName: r.ProfileName, Succeeded: r.Succeeded, ErrorCode: r.Error, Streaming: r.Streaming, Input: r.Input, Read: r.Read, Write: r.Write, Output: r.Output, InputPrice: r.InputPrice, OutputPrice: r.OutputPrice, Multiplier: r.Multiplier, Tps: r.TPS, Elapsed: r.Elapsed, CostMicro: r.Cost, CostsMicro: r.Costs[:]})
	}
	return out, usageError(e)
}

// A log page is one SQLite snapshot. Never tag that page with a deletion/reprice
// generation observed only after its read; fail once so the caller can read a new page.
func readAIUsageLogs(ctx context.Context, list func(context.Context, aiusage.Query) (aiusage.Logs, error), state func() aiusage.State, q aiusage.Query) (aiusage.Logs, aiusage.State, error) {
	before := state()
	rows, err := list(ctx, q)
	after := state()
	if err != nil {
		return aiusage.Logs{}, after, err
	}
	if before.Generation != after.Generation {
		return aiusage.Logs{}, after, aiusage.ErrBusy
	}
	return rows, after, nil
}

// RepriceAIUsage charges stored history at the Profile's current multipliers. The
// multipliers come from the Profile store, never from the request, so the client cannot
// price history at a value the Profile does not hold.
func (s *AIUsageServer) RepriceAIUsage(ctx context.Context, r *pb.RepriceAIUsageRequest) (*pb.RepriceAIUsageResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if s.profiles == nil {
		return nil, status.Error(codes.Unavailable, "ai_usage_unavailable")
	}
	if r.ProfileId == "" || r.ModelId != nil && *r.ModelId == "" {
		return nil, usageError(aiusage.ErrRange)
	}
	m, models, found, e := s.profiles.Pricing(ctx, r.ProfileId)
	if e != nil {
		return nil, usageError(e)
	}
	if !found {
		return nil, status.Error(codes.NotFound, "ai_profile_not_found")
	}
	q := aiusage.RepriceQuery{ProfileID: r.ProfileId, FromDay: r.FromDay, ToDay: r.ToDay, All: r.All, DryRun: r.DryRun, Multiplier: m, Models: models}
	if r.ModelId != nil {
		q.Model = *r.ModelId
	}
	v, e := s.store.Reprice(ctx, q)
	if e != nil {
		return nil, usageError(e)
	}
	return &pb.RepriceAIUsageResult{Requests: v.Requests, AggregatedRequests: v.Aggregated, OldCostMicro: v.OldCost, NewCostMicro: v.NewCost, DryRun: r.DryRun, OverrideId: v.OverrideID, State: usageState(s.store.State())}, nil
}
