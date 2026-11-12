package controller

import (
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/aiusage"
)

func limitMoney(v aiusage.LimitMoney) *pb.AIUsageLimitMoney {
	return &pb.AIUsageLimitMoney{Official: v.Official, Channel: v.Channel}
}
func optionalLimitMoney(v *aiusage.LimitMoney) *pb.AIUsageLimitMoney {
	if v == nil {
		return nil
	}
	return limitMoney(*v)
}
func modelLimit(v *aiusage.ModelLimit) *pb.AIUsageModelLimit {
	if v == nil {
		return nil
	}
	return &pb.AIUsageModelLimit{Model: v.Model, Limit: v.Limit, Warn: v.Warn, FlatLimit: v.FlatLimit, FlatWarn: v.FlatWarn, Baseline: v.Baseline}
}
func limitBands(bands [3]aiusage.LimitBand) []*pb.AIUsageLimitBand {
	out := make([]*pb.AIUsageLimitBand, 0, len(bands))
	for _, band := range bands {
		out = append(out, &pb.AIUsageLimitBand{Requests: band.Requests, PricedRequests: band.PricedRequests, Money: limitMoney(band.Money)})
	}
	return out
}
func limitRows(rows []aiusage.LimitRow) []*pb.AIUsageLimitRow {
	var out []*pb.AIUsageLimitRow
	for _, r := range rows {
		v := &pb.AIUsageLimitRow{Id: r.ID, Cli: r.CLI, SessionId: r.Session, Title: r.Title, Model: r.Model, Effort: r.Effort, Emphasized: r.Emphasized, Note: r.Note, Lines: modelLimit(r.Lines), Multiple: r.Multiple, OverSpend: optionalLimitMoney(r.OverSpend), Unpriced: r.Unpriced}
		v.Bands = limitBands(r.Bands)
		for _, l := range r.ModelLines {
			v.ModelLines = append(v.ModelLines, &pb.AIUsageLimitModelLine{Model: l.Model, Lines: modelLimit(l.Lines), Requests: l.Requests})
		}
		if f := r.Flat; f != nil {
			v.Flat = &pb.AIUsageLimitFlat{Limit: f.Limit, Requests: f.Requests, Cost: limitMoney(f.Cost), OverSpend: optionalLimitMoney(f.OverSpend)}
		}
		out = append(out, v)
	}
	return out
}
func sessionLimits(v *aiusage.SessionLimits) *pb.AIUsageSessionLimits {
	if v == nil {
		return nil
	}
	s := v.Summary
	out := &pb.AIUsageSessionLimits{Primary: limitRows(v.Primary), Secondary: limitRows(v.Secondary), AgentsIncomplete: v.AgentsIncomplete, Summary: &pb.AIUsageLimitSummary{Input: limitMoney(s.Input), OverSpend: limitMoney(s.OverSpend), Sessions: s.Sessions, OverSessions: s.OverSessions, Requests: s.Requests, Turns: s.Turns, TurnsComplete: s.TurnsComplete, Subagents: s.Subagents, Curve: s.Curve}}
	out.Summary.InputTokens = &s.InputTokens
	out.Summary.Bands = limitBands(s.Bands)
	out.Summary.Multiple = s.Multiple
	out.Summary.ObservedThroughMs = s.ObservedThrough
	for _, day := range s.Days {
		out.Summary.Days = append(out.Summary.Days, &pb.AIUsageLimitDay{AtMs: day.At, Sessions: day.Sessions, Input: limitMoney(day.Input), Requests: day.Requests, PricedRequests: day.PricedRequests, Available: day.Available, InputTokens: &day.InputTokens})
	}
	if c := s.Comparison; c != nil {
		out.Summary.Comparison = &pb.AIUsageLimitComparison{Input: limitMoney(c.Input), Sessions: c.Sessions, FromMs: c.From, ToMs: c.To, InputTokens: &c.InputTokens}
	}
	if c := s.Current; c != nil {
		out.Summary.Current = &pb.AIUsageLimitCurrent{Context: c.Context, Limit: c.Limit, Band: c.Band}
	}
	if t := v.Trend; t != nil {
		out.Trend = &pb.AIUsageLimitTrend{Lines: modelLimit(t.Lines), OverStart: t.OverStart, FlatStart: t.FlatStart, HasWriteLayer: t.HasWriteLayer}
		for _, r := range t.Requests {
			a := r.Request
			out.Trend.Requests = append(out.Trend.Requests, &pb.AIUsageLimitRequest{Request: &pb.AIUsageAnalysisRow{Cli: a.CLI, SessionId: a.Session, SessionTitle: a.SessionTitle, RequestId: a.RequestID, TurnId: a.Turn, ModelId: a.Model, ReasoningEffort: a.Effort, AtMs: a.At, Totals: usageTotals(a.Summary), MaxInput: a.MaxInput, KnownTurnRequests: a.KnownTurnRequests, PricedRequests: a.PricedRequests}, Lines: modelLimit(r.Lines), Band: r.Band, TurnNumber: r.TurnNumber})
		}
		for _, m := range t.Switches {
			out.Trend.Switches = append(out.Trend.Switches, &pb.AIUsageLimitSwitch{Index: m.Index, Kind: m.Kind, From: m.From, To: m.To, TurnMultiple: m.TurnMultiple, AfterReadMultiple: m.AfterReadMultiple})
		}
	}
	return out
}
