package controller

import (
	"context"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/aiusage"
)

func catalogEntry(v aiusage.SessionCatalogEntry) *pb.AISessionCatalogEntry {
	return &pb.AISessionCatalogEntry{Key: &pb.AIUsageSessionKey{Cli: v.Key.CLI, SessionId: v.Key.Session}, Title: v.Title, TitleSource: v.TitleSource, TitleStatus: v.Status, LastActivityMs: v.LastActivity, UnlinkedChild: v.Unlinked}
}

// ListAISessions filters indexed native activity before counting and pagination.
func (s *AIUsageServer) ListAISessions(ctx context.Context, q *pb.AISessionCatalogQuery) (*pb.AISessionCatalogPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	query := aiusage.SessionCatalogQuery{CLI: q.Cli, Text: q.Query, PageToken: q.PageToken, PageSize: int(q.PageSize), From: q.FromMs, To: q.ToMs}
	if q.Selected != nil {
		query.Selected = &aiusage.SessionKey{CLI: q.Selected.Cli, Session: q.Selected.SessionId}
	}
	page, err := s.store.ListSessions(ctx, query)
	if err != nil {
		return nil, usageError(err)
	}
	out := &pb.AISessionCatalogPage{Revision: page.Revision, UsageRevision: page.UsageRevision, Ready: page.Ready, Total: page.Total, NextPageToken: page.NextPageToken, ResetRequired: page.ResetRequired}
	for _, v := range page.Items {
		out.Items = append(out.Items, catalogEntry(v))
	}
	if page.Selected != nil {
		out.Selected = catalogEntry(*page.Selected)
	}
	for _, cli := range []string{"claude", "codex", "dsh"} {
		out.Counts = append(out.Counts, &pb.AISessionCatalogCount{Cli: cli, Count: page.Counts[cli]})
		if code := page.Sources[cli]; code != "" {
			out.Sources = append(out.Sources, &pb.AISessionCatalogSource{Cli: cli, ErrorCode: code})
		}
	}
	return out, nil
}
