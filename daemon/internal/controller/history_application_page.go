package controller

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/historystore"
)

func applicationWorkbenchPage(q *pb.LogQuery) bool {
	if !q.ApplicationWorkbenchPage || q.View != "activity" || q.Scope != "access" ||
		q.Dimension != "application" || q.RouteId != "" || q.ProblemId != "" ||
		q.ConnectionKey != "" || q.Quick != "" && q.Quick != "all" {
		return false
	}
	c := q.Criteria
	return c == nil || (len(c.Kinds) == 0 && len(c.Levels) == 0 && len(c.Transports) == 0 &&
		len(c.Directions) == 0 && c.Application == "" && c.Domain == "" && c.TargetIp == "" &&
		c.Route == "" && c.RulePackage == "" && c.DnsAnswerIp == "" && c.Port == 0 &&
		c.MinLatencyMs == 0)
}

func logPageKeys(ctx context.Context, read *columnLogReadState, selection *historystore.ColumnLogSelection,
	q *pb.LogQuery, filter historystore.ColumnLogFilter, cap int, signature string, out *pb.LogPage,
) ([]string, error) {
	if applicationWorkbenchPage(q) {
		return applicationPageKeys(ctx, read, selection, q, filter, signature, out)
	}
	total, err := selection.Count(ctx, filter)
	if err != nil {
		return nil, err
	}
	out.Total = total
	start, end, err := historyBoundPage(q.Cursor, int(total), int(q.Limit), cap, signature)
	if err != nil {
		return nil, err
	}
	out.NextCursor = historyBoundNext(end, int(total), signature)
	return selection.KeysPage(ctx, filter, start, end-start)
}

func applicationPageKeys(ctx context.Context, read *columnLogReadState, selection *historystore.ColumnLogSelection,
	q *pb.LogQuery, filter historystore.ColumnLogFilter, signature string, out *pb.LogPage,
) ([]string, error) {
	var after *historystore.ActivityPageKey
	if q.Cursor != "" {
		var err error
		after, err = parseApplicationPageCursor(q.Cursor, signature, q.ReadToken)
		if err != nil {
			return nil, err
		}
	}
	if read.workbenchSignature != signature {
		total, err := selection.Count(ctx, filter)
		if err != nil {
			return nil, err
		}
		read.workbenchSignature, read.workbenchTotal = signature, total
	}
	out.Total = read.workbenchTotal
	limit := int(q.Limit)
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	offset := 0
	if q.RecoveryOffset > 0 && after == nil && q.ReadToken == "" && out.Total > 0 {
		offset = min(int(q.RecoveryOffset)/limit, (int(out.Total)-1)/limit) * limit
	}
	page, err := selection.ActivityPage(ctx, filter, after, offset, limit+1)
	if err != nil {
		return nil, err
	}
	if len(page) > limit {
		page = page[:limit]
		out.NextCursor = applicationPageCursor(page[len(page)-1], signature, out.ReadToken)
	}
	keys := make([]string, 0, len(page))
	for _, row := range page {
		keys = append(keys, row.Key)
	}
	return keys, nil
}

func applicationPageCursor(row historystore.ActivityPageKey, signature, token string) string {
	return "key." + signature[:12] + "." + token + "." + strconv.FormatInt(row.LastNS, 10) +
		"." + base64.RawURLEncoding.EncodeToString([]byte(row.Key))
}

func parseApplicationPageCursor(cursor, signature, token string) (*historystore.ActivityPageKey, error) {
	parts := strings.SplitN(cursor, ".", 5)
	if len(parts) != 5 || parts[0] != "key" || parts[1] != signature[:12] ||
		token == "" || parts[2] != token {
		return nil, errors.New("history_cursor_expired")
	}
	ns, err := strconv.ParseInt(parts[3], 10, 64)
	key, keyErr := base64.RawURLEncoding.DecodeString(parts[4])
	if err != nil || keyErr != nil || len(key) == 0 {
		return nil, errors.New("history_cursor_expired")
	}
	return &historystore.ActivityPageKey{LastNS: ns, Key: string(key)}, nil
}
