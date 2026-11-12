package aiusage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSubscriptionLogsAreCollectedWithoutAnyProfile(t *testing.T) {
	for _, provider := range []string{"openai", "Sub2API"} {
		t.Run(provider, func(t *testing.T) {
			h := home(t)
			now := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
			writeFixture(t, filepath.Join(h, ".claude", "projects", "p", "a.jsonl"), "claude-redacted.jsonl")
			raw, err := os.ReadFile("../../../tests/ai-usage/codex-redacted.jsonl")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(h, ".codex", "sessions", "2025", "rollout-fixture.jsonl")
			if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, []byte(strings.ReplaceAll(string(raw), `"model_provider":"Sub2API"`, `"model_provider":"`+provider+`"`)), 0600); err != nil {
				t.Fatal(err)
			}
			s := openAt(t, h, &now, func(string, time.Time) Attribution {
				return Attribution{ServiceSource: "official_account", Since: time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC).UnixMilli(), Multiplier: 1}
			})
			defer func() { _ = s.Close(context.Background()) }()
			if err = s.Scan(context.Background()); err != nil {
				t.Fatal(err)
			}
			logs, err := s.List(context.Background(), Query{From: now.Add(-48 * time.Hour).UnixMilli(), To: now.UnixMilli()})
			if err != nil || logs.All == 0 {
				t.Fatal("no subscription usage collected", err)
			}
			seen := map[string]bool{}
			for _, r := range logs.Requests {
				want := "official_account"
				if r.CLI == "codex" && provider != "openai" {
					want = "unknown"
				}
				if r.ProfileID != "" || r.Multiplier != 1 || r.Details.ServiceSource != want {
					t.Fatalf("subscription misattributed %+v", r)
				}
				seen[r.CLI] = true
			}
			if !seen["claude"] || !seen["codex"] {
				t.Fatal("one CLI was not collected", seen)
			}
			if logs.Total.UnconfirmedChannel != logs.All {
				t.Fatal("equivalent cost advertised as channel bill")
			}
		})
	}
}

func TestSourcesKeepAccountAndUnknownOutOfOldRelayPricing(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	since := now.Add(-time.Hour).UnixMilli()
	for _, source := range []string{"official_account", "official_api", "relay_api", "unknown"} {
		t.Run(source, func(t *testing.T) {
			resolver := attributor{resolve: func(string, time.Time) Attribution {
				return Attribution{ServiceSource: source, Since: since, Multiplier: 1}
			}}
			r := Request{CLI: "claude", Key: "new", Model: "claude-sonnet-4-5-20250929", At: now.UnixMilli(), Input: 1000000, sourceStart: since + 1}
			r.attribute(resolver)
			if r.ProfileID != "" || r.Multiplier != 1 || r.Details.ServiceSource != source || r.Cost != 3000000 {
				t.Fatalf("wrong source price %+v", r)
			}
			old := r
			old.sourceStart = since - 1
			old.attribute(resolver)
			if old.Details.ServiceSource != "unknown" || old.ProfileID != "" || old.Multiplier != 1 {
				t.Fatalf("claimed an old process had switched %+v", old)
			}
		})
	}
}

func TestSameHourSourcesStaySeparateAndAccountCollectionNeedsNoProfile(t *testing.T) {
	h := home(t)
	now := time.Date(2026, 11, 12, 12, 30, 0, 0, time.UTC)
	s := openAt(t, h, &now, nil)
	defer func() { _ = s.Close(context.Background()) }()
	var rows []Request
	for i, source := range []string{"official_account", "official_api", "relay_api", "unknown"} {
		r := Request{CLI: "claude", Key: source, Session: source, Model: "claude-sonnet-4-5-20250929", At: now.UnixMilli() + int64(i), Input: 1000000, Succeeded: true, Multiplier: 1, Details: Details{ServiceSource: source}}
		r.price()
		rows = append(rows, r)
	}
	ctx := context.Background()
	if err := s.commit(ctx, rows, cursor{Path: "fixture"}); err != nil {
		t.Fatal(err)
	}
	buckets, err := s.Summarize(ctx)
	if err != nil || len(buckets) != 4 {
		t.Fatalf("mixed sources merged: %d %v", len(buckets), err)
	}
	for _, source := range []string{"official_account", "official_api", "relay_api"} {
		logs, err := s.List(ctx, Query{From: now.Add(-time.Hour).UnixMilli(), To: now.Add(time.Hour).UnixMilli(), Profile: "__source:" + source + "__"})
		if err != nil || logs.All != 1 || logs.Requests[0].Details.ServiceSource != source {
			t.Fatal("source filter differs from accounting", source, err)
		}
	}
	logs, err := s.List(ctx, Query{From: now.Add(-time.Hour).UnixMilli(), To: now.Add(time.Hour).UnixMilli()})
	if err != nil || logs.Total.UnconfirmedChannel != 2 {
		t.Fatal("account/unknown advertised as channel costs", logs.Total, err)
	}
	analysis, err := s.AnalyzeLimits(ctx, now.Add(-time.Hour).UnixMilli(), now.Add(time.Hour).UnixMilli(), nil)
	if err != nil || !analysis.Limits.Summary.Input.ChannelUnknown {
		t.Fatal("session summary advertised account cost as channel cost", err)
	}
}

func TestHourlySourceMigrationPreservesLegacyLedger(t *testing.T) {
	h := home(t)
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	s := openAt(t, h, &now, nil)
	dir := s.options.Directory
	r := Request{CLI: "claude", Key: "legacy", ProfileID: "profile", ProfileName: "Kept", Model: "claude-sonnet-4-5-20250929", At: now.UnixMilli(), Input: 1000000, Multiplier: .3, Succeeded: true}
	r.price()
	if err := s.commit(context.Background(), []Request{r}, cursor{Path: "legacy"}); err != nil {
		t.Fatal(err)
	}
	before := s.State()
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "ai_usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	legacySchema := strings.Replace(hourlySchema, "service_source TEXT NOT NULL DEFAULT '',unconfirmed_channel INTEGER NOT NULL DEFAULT 0,", "", 1)
	legacySchema = strings.Replace(legacySchema, "PRIMARY KEY(hour,cli,profile_id,model,service_source)", "PRIMARY KEY(hour,cli,profile_id,model)", 1)
	columns := "hour,cli,profile_id,model,profile_name," + strings.TrimSuffix(summaryColumns, ",unconfirmed_channel")
	if _, err = db.Exec("ALTER TABLE usage_hourly RENAME TO newer;" + legacySchema + "INSERT INTO usage_hourly(" + columns + ") SELECT " + columns + " FROM newer; DROP TABLE newer; PRAGMA user_version=11;"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	s, err = Open(context.Background(), Options{Home: h, Directory: dir, Owner: uint32(os.Geteuid()), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(context.Background()) }()
	logs, err := s.List(context.Background(), Query{From: now.Add(-time.Hour).UnixMilli(), To: now.Add(time.Hour).UnixMilli()})
	if err != nil || logs.All != 1 || logs.Requests[0].Cost != r.Cost || logs.Requests[0].ProfileID != r.ProfileID || logs.Requests[0].Multiplier != r.Multiplier {
		t.Fatal("migration changed legacy request", logs, err)
	}
	buckets, err := s.Summarize(context.Background())
	if err != nil || len(buckets) != 1 || buckets[0].ServiceSource != "" || buckets[0].Cost != r.Cost || buckets[0].Input != r.Input {
		t.Fatal("migration changed old aggregates", buckets, err)
	}
	if s.State().Revision != before.Revision || s.State().Generation != before.Generation {
		t.Fatal("structure-only migration changed usage versions")
	}
}

func TestRebuildKeepsRecordedSourceAndRoundedOlderBucketAmounts(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	old := openAt(t, home(t), &now, nil)
	defer func() { _ = old.Close(ctx) }()
	request := Request{CLI: "claude", Key: "older-recorded-route", ProfileID: "p", ProfileName: "Relay", Model: "claude-sonnet-4-5-20250929", At: now.AddDate(0, 0, -100).UnixMilli(), Multiplier: .265, Input: 12347, Read: 2123, Output: 173, Details: Details{ServiceSource: "relay_api"}}
	request.price()
	if err := old.commit(ctx, []Request{request}, cursor{Path: "old-fixture"}); err != nil {
		t.Fatal(err)
	}
	next := openAt(t, home(t), &now, nil)
	defer func() { _ = next.Close(ctx) }()
	attr := attributor{resolve: func(string, time.Time) Attribution {
		return Attribution{ServiceSource: "official_account", Multiplier: 1}
	}}
	var report Report
	if err := next.keepOlderBuckets(ctx, old.r, attr, now.AddDate(0, 0, -93).UnixMilli(), &report); err != nil {
		t.Fatal(err)
	}
	rows, err := next.Summarize(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatal("older source lost", err)
	}
	if rows[0].ProfileID != "p" || rows[0].ServiceSource != "relay_api" || rows[0].Cost != request.Cost || rows[0].BilledCosts != request.Details.Billed {
		t.Fatalf("rebuild relabelled or changed rounding: %+v", rows[0])
	}
}
