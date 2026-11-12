package controller

import (
	"context"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"sort"
	"testing"
	"testing/synctest"
	"time"
)

// All expected fields come from the e9d5c7c bbolt oracle. The exact fixture clock
// also fixes calendar boundaries and the last point's duration. Complete protobuf
// equality is bidirectional: extra rows, samples, or trend points fail the test.
func TestTrafficSQLQueryMatchesLegacyProjectionFieldByField(t *testing.T) {
	queryGoldenTimezone(t)
	synctest.Test(t, func(t *testing.T) {
		h := newTrafficHarness(t)
		at := time.Now()
		h.clock = at
		c := New("golden", "golden")
		c.traffic = h.tr
		h.tr.route = c.mapHistoryRoute
		for _, records := range queryGoldenFixture(at) {
			h.seq = records[len(records)-1].Sequence
			h.append(at, true, records...)
		}
		h.settle()
		for _, w := range []string{"today", "week", "month"} {
			// Source/target rows now merge all exits and report package summaries;
			// their new behavior is checked by TestTrafficAllDimensionsMergeExitsAndKeepSourcesExclusive.
			// Keep the unchanged IP/network views against the historical byte-for-byte oracle.
			for _, d := range []string{"ip", "network"} {
				for _, filter := range []string{"all", "direct", "proxy", "search"} {
					name := w + "-" + d + "-" + filter
					t.Log(name)
					{
						q := &pb.TrafficQuery{Window: w, Dimension: d, Until: timestamppb.New(at)}
						if filter == "search" {
							q.Search = "Route A"
						} else {
							q.Direction = filter
						}
						c.readTokens = newReadTokenCache()
						r, _, e := c.aggregateTrafficRead(context.Background(), q)
						if e != nil {
							t.Fatal(e)
						}
						want := &pb.TrafficPage{}
						readHistoryGoldenPB(t, "traffic-query/"+name+"/page.pb", want)
						if filter == "search" && d != "network" {
							want = wholeObjectGoldenSearch(t, w, d, want)
						}
						if d == "system" && (filter == "direct" || filter == "proxy") {
							completeDNS := &pb.TrafficPage{}
							readHistoryGoldenPB(t, "traffic-query/"+w+"-system-all/page.pb", completeDNS)
							want.DnsSummary = proto.Clone(completeDNS.DnsSummary).(*pb.TrafficRow)
						}
						remapped := map[string]string{}
						if d == "domain" || d == "ip" {
							for _, row := range want.Items {
								oldID := row.Id
								row.Id = aggregateRowID(aggregateIdentity{Dimension: d, Object: row.ObjectId, Route: row.RouteId, Direction: row.Direction})
								row.SourceKind, row.SourceLabel, row.SourceEvidence, row.Kind = "mixed", "", "目标视角包含多种来源", "access"
								remapped[oldID] = row.Id
							}
							sort.Slice(want.Items, func(i, j int) bool {
								a, b := want.Items[i], want.Items[j]
								if a.LastActiveAt != b.LastActiveAt {
									return a.LastActiveAt > b.LastActiveAt
								}
								if a.Upload+a.Download != b.Upload+b.Download {
									return a.Upload+a.Download > b.Upload+b.Download
								}
								return a.Id < b.Id
							})
						}
						got := queryGoldenPage(r)
						// The legacy oracle predates retained-response series. Compare its complete
						// aggregate contract; the new independent fields have their own RPC tests.
						for _, row := range append(append([]*pb.TrafficRow(nil), got.Items...), got.UnknownSource, got.DnsSummary) {
							if row != nil {
								row.RecentResponses = nil
								row.RecentResponsesState = ""
								row.RecentResponsesError = ""
							}
						}
						if !proto.Equal(want, got) {
							t.Fatalf("%s complete page differs:\nwant %v\ngot %v", name, want, got)
						}
						if d == "network" {
							continue
						} // Network has no object rows or per-object detail in its product page.
						goldenName := name
						if filter == "search" {
							goldenName = w + "-" + d + "-all"
						}
						files, e := filepath.Glob(filepath.Join("testdata/history-legacy/traffic-query", goldenName, "*.pb"))
						if e != nil {
							t.Fatal(e)
						}
						expected := map[string][]*pb.TrafficPoint{}
						for _, p := range files {
							if filepath.Base(p) == "page.pb" {
								continue
							}
							raw, e := os.ReadFile(p)
							if e != nil {
								t.Fatal(e)
							}
							v := &pb.TrafficPage{}
							if e = proto.Unmarshal(raw, v); e != nil {
								t.Fatal(e)
							}
							id := filepath.Base(p)[:len(filepath.Base(p))-3]
							if mapped, ok := remapped[id]; ok {
								id = mapped
							}
							if filter == "search" {
								visible := false
								for _, row := range want.Items {
									visible = visible || row.Id == id
								}
								visible = visible || want.UnknownSource != nil && want.UnknownSource.Id == id || want.DnsSummary != nil && want.DnsSummary.Id == id
								if !visible {
									continue
								}
							}
							if d == "system" && (filter == "direct" || filter == "proxy") && want.DnsSummary != nil && id == want.DnsSummary.Id {
								complete := &pb.TrafficPage{}
								readHistoryGoldenPB(t, "traffic-query/"+w+"-system-all/"+id+".pb", complete)
								v.Trend = complete.Trend
							}
							expected[id] = v.Trend
						}
						if len(expected) != len(r.aggregateTrend) {
							t.Fatalf("trend row sets differ: %d / %d", len(expected), len(r.aggregateTrend))
						}
						for id, points := range expected {
							actual, ok := r.aggregateTrend[id]
							if !ok || len(points) != len(actual) {
								t.Fatalf("trend %s missing or length differs", id)
							}
							for i, p := range points {
								if !proto.Equal(p, actual[i]) {
									t.Fatalf("trend %s point %d differs", id, i)
								}
							}
						}
						if filter == "all" && d == "application" {
							if want.UnknownSource == nil || want.SystemUpload == 0 {
								t.Fatal("fixture lost unknown/system subtotal coverage")
							}
						}
						if filter == "all" && d == "system" && want.DnsSummary == nil {
							t.Fatal("fixture lost DNS merge coverage")
						}
						if filter == "all" && d == "domain" && len(want.Items) < 2 {
							t.Fatal("fixture lost route coverage")
						}
					}
				}
			}
		}
	})
}

// User-confirmed search selects complete rows. Expected memberships remain the
// old search golden; all numerical/latency/trend fields come from its unfiltered
// e9d5c7c golden, never from the implementation under test.
func wholeObjectGoldenSearch(t *testing.T, window, dimension string, matched *pb.TrafficPage) *pb.TrafficPage {
	t.Helper()
	full := &pb.TrafficPage{}
	readHistoryGoldenPB(t, "traffic-query/"+window+"-"+dimension+"-all/page.pb", full)
	wanted := map[string]bool{}
	for _, row := range matched.Items {
		wanted[row.Id] = true
	}
	full.Items = filterGoldenRows(full.Items, wanted)
	if matched.UnknownSource == nil {
		full.UnknownSource = nil
	}
	if matched.DnsSummary == nil {
		full.DnsSummary = nil
	}
	full.Upload, full.Download, full.Connections = 0, 0, 0
	// The fixture's system objects have distinct routes; this accepted search
	// change only expands the merged unknown/DNS objects.
	full.SystemUpload, full.SystemDownload = matched.SystemUpload, matched.SystemDownload
	for _, row := range full.Items {
		full.Upload += row.Upload
		full.Download += row.Download
		full.Connections += row.Connections
	}
	for _, point := range full.Trend {
		point.Upload, point.Download = 0, 0
	}
	for _, row := range full.Items {
		trend := &pb.TrafficPage{}
		readHistoryGoldenPB(t, "traffic-query/"+window+"-"+dimension+"-all/"+row.Id+".pb", trend)
		for i, point := range trend.Trend {
			full.Trend[i].Upload += point.Upload
			full.Trend[i].Download += point.Download
			if point.Upload+point.Download > 0 {
				full.Trend[i].Gap = false
			}
			full.Trend[i].Partial = full.Trend[i].Partial || point.Partial
		}
	}
	return full
}
func filterGoldenRows(rows []*pb.TrafficRow, wanted map[string]bool) []*pb.TrafficRow {
	var out []*pb.TrafficRow
	for _, row := range rows {
		if wanted[row.Id] {
			out = append(out, row)
		}
	}
	return out
}
