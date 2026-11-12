package controller

import (
	"encoding/json"
	"fmt"
	"google.golang.org/protobuf/proto"
	"os"
	"path/filepath"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
	"testing"
)

func readHistoryGolden(t *testing.T, name string, v any) {
	t.Helper()
	raw, e := os.ReadFile(filepath.Join("testdata/history-legacy", name))
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, v); e != nil {
		t.Fatal(e)
	}
}
func readHistoryGoldenPB(t *testing.T, name string, v proto.Message) {
	t.Helper()
	raw, e := os.ReadFile(filepath.Join("testdata/history-legacy", name))
	if e != nil {
		t.Fatal(e)
	}
	if e = proto.Unmarshal(raw, v); e != nil {
		t.Fatal(e)
	}
}
func readGoldenLogState(t *testing.T, name string) (map[string]config.LogConnectionSummary, []config.HistoryEvent, map[string]string) {
	t.Helper()
	s := map[string]config.LogConnectionSummary{}
	var keys []string
	readHistoryGolden(t, name+"/summary-keys.json", &keys)
	for _, k := range keys {
		var v config.LogConnectionSummary
		readHistoryGolden(t, fmt.Sprintf("%s/summaries/%s.json", name, aggregateID(aggregateIdentity{Object: k})), &v)
		s[k] = v
	}
	var n int
	readHistoryGolden(t, name+"/event-count.json", &n)
	var events []config.HistoryEvent
	for i := 0; i < n; i++ {
		var v config.HistoryEvent
		readHistoryGolden(t, fmt.Sprintf("%s/events/%02d.json", name, i), &v)
		events = append(events, v)
	}
	d := map[string]string{}
	readHistoryGolden(t, name+"/diagnostics.json", &d)
	return s, events, d
}
func readGoldenTraffic(t *testing.T) (map[historystore.TrafficKey]*aggregateBucket, map[historystore.TrafficKey][]string) {
	t.Helper()
	rows := map[historystore.TrafficKey]*aggregateBucket{}
	kinds := map[historystore.TrafficKey][]string{}
	var names []string
	readHistoryGolden(t, "traffic-write/keys.json", &names)
	for _, name := range names {
		var v struct {
			Key   historystore.TrafficKey
			Value aggregateBucket
			Kinds []string
		}
		readHistoryGolden(t, "traffic-write/"+name+".json", &v)
		rows[v.Key] = &v.Value
		kinds[v.Key] = v.Kinds
	}
	return rows, kinds
}
