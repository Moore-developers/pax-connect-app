package controller

import (
	"context"
	"testing"

	"pax-connect/daemon/internal/historystore"
)

// readTrafficTable returns every row of traffic_5m or recent_5s.
func readTrafficTable(t *testing.T, store *historystore.Traffic, table string) []historystore.TrafficRow {
	t.Helper()
	rows, err := store.Reader().Query(`SELECT bucket_ms,dimension,object,route,direction,name,source_kind,route_name,up_bytes,down_bytes,new_conns,failures,
latency_ms,latency_at_ms,max_latency_ms,latency_hist,rule_basis,search FROM ` + table + ` ORDER BY bucket_ms,dimension,object,route,direction`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []historystore.TrafficRow
	for rows.Next() {
		var r historystore.TrafficRow
		if err = rows.Scan(&r.BucketMs, &r.Dimension, &r.Object, &r.Route, &r.Direction, &r.Name, &r.SourceKind, &r.RouteName, &r.UpBytes, &r.DownBytes,
			&r.NewConns, &r.Failures, &r.LatencyMs, &r.LatencyAtMs, &r.MaxLatencyMs, &r.LatencyHist, &r.RuleBasis, &r.Search); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// readCoverage returns the bitmap of every 5-minute bucket, keyed by bucket start in ms.
func readCoverage(t *testing.T, store *historystore.Traffic) map[int64][]byte {
	t.Helper()
	rows, err := store.Reader().Query(`SELECT bucket_ms,bits FROM coverage_5m`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int64][]byte{}
	for rows.Next() {
		var at int64
		var bits []byte
		if err = rows.Scan(&at, &bits); err != nil {
			t.Fatal(err)
		}
		out[at] = bits
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func trafficMeta(t *testing.T, store *historystore.Traffic, key string) string {
	t.Helper()
	v, _, err := store.Meta(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
