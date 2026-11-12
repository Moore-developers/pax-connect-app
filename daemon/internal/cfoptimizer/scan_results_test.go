package cfoptimizer

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"
)

func TestScanPublishesOnlySuccessfulPrecisionSamples(t *testing.T) {
	var got []Record
	err := Scan(context.Background(), "A", Probe{TCP: func(_ context.Context, ip string) (time.Duration, error) {
		if ip == "104.16.0.1" {
			return 90 * time.Millisecond, nil
		}
		return 0, errors.New("unreachable")
	}}, rand.New(rand.NewSource(1)), func(r Record) { got = append(got, r) }, func(Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].IP != "104.16.0.1" || got[0].TCP.Rounds != 3 || got[0].TCP.Value != 90 {
		t.Fatalf("coarse or failed samples leaked into scan results: %d records", len(got))
	}
}

func TestScanDocumentKeepsFastestThousandAndNoOldStages(t *testing.T) {
	at := time.Now()
	var records []Record
	for i := 0; i < 1100; i++ {
		ip := fmt.Sprintf("104.16.%d.%d", i/256, i%256)
		records = append(records, Record{IP: ip, Network: "A", Reachable: true, TCP: Sample{Value: float64(i + 1), At: at, Attempt: at, Rounds: 3}, Download: Sample{Value: 99, At: at}, Location: Location{Country: "old"}, ValidationKey: "old"})
	}
	records = append(records, Record{IP: "104.17.0.1", Network: "A", TCP: Sample{Value: 0.1, At: at, Error: "cf_tcp_unreachable", Rounds: 3}})
	got := ScanDocument("A", records, at)
	raw, err := Encode(got)
	if err != nil {
		t.Fatal(err)
	}
	got, err = Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Records) != 1000 || got.Records[0].TCP.Value != 1 || got.Records[999].TCP.Value != 1000 {
		t.Fatal("kept latest instead of fastest samples")
	}
	for _, r := range got.Records {
		if r.Download.Value != 0 || r.ValidationKey != "" || r.Location.Country != "" {
			t.Fatal("old stage survived")
		}
	}
	if !got.Accepts(got.Records[0].IP, "A") || got.Accepts("104.17.0.1", "A") || got.Accepts(got.Records[0].IP, "B") {
		t.Fatal("snapshot membership not enforced")
	}
}
