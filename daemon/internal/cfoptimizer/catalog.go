package cfoptimizer

import (
	"encoding/json"
	"errors"
	"math"
	"net/netip"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Sample separates the last successful value from the most recent attempt.
type Sample struct {
	Value   float64   `json:"value"`
	At      time.Time `json:"at"`
	Attempt time.Time `json:"attempt"`
	Error   string    `json:"error,omitempty"`
	Rounds  int       `json:"rounds"`
}

// Location is a database estimate for the specified IP, not an anycast edge.
type Location struct {
	CountryCode, Country, Region, City, Source, Error string
	At                                                time.Time
}

// Record contains independent physical-edge and full-route observations.
type Record struct {
	IP, Prefix, Network       string
	Location                  Location
	TCP, Download, Validation Sample
	Reachable                 bool
	Issue                     string
	ValidationKey             string
	ScannedAt, CheckedAt      time.Time
}

// Document is the bounded, versioned evidence catalog. It contains no credentials.
type Document struct {
	Version     int
	Records     []Record
	ScanNetwork string    `json:",omitempty"`
	ScanAt      time.Time `json:",omitempty"`
}

func NewDocument() Document { return Document{Version: 1, Records: []Record{}} }

// Observe keeps previous successful values on failure without publishing current success.
func Observe(previous Sample, value float64, at time.Time, code string, rounds int) Sample {
	previous.Attempt = at
	previous.Error = code
	if code == "" && value > 0 && !math.IsInf(value, 0) && !math.IsNaN(value) {
		previous.Value = value
		previous.At = at
		previous.Rounds = rounds
	}
	return previous
}

// Merge updates one stage without erasing other stages, and applies retention limits.
func (d *Document) Merge(in Record, now time.Time) {
	index := -1
	for i, r := range d.Records {
		if r.IP == in.IP && r.Network == in.Network {
			index = i
			break
		}
	}
	if index < 0 {
		d.Records = append(d.Records, Record{IP: in.IP, Prefix: in.Prefix, Network: in.Network})
		index = len(d.Records) - 1
	}
	r := &d.Records[index]
	if !in.Location.At.IsZero() {
		r.Location = in.Location
	}
	if !in.TCP.Attempt.IsZero() {
		r.TCP = Observe(r.TCP, in.TCP.Value, in.TCP.Attempt, in.TCP.Error, in.TCP.Rounds)
		r.Reachable = in.TCP.Error == ""
		r.CheckedAt = in.TCP.Attempt
		r.Issue = in.TCP.Error
	}
	if !in.Download.Attempt.IsZero() {
		r.Download = Observe(r.Download, in.Download.Value, in.Download.Attempt, in.Download.Error, in.Download.Rounds)
	}
	if !in.Validation.Attempt.IsZero() {
		r.Validation = Observe(r.Validation, in.Validation.Value, in.Validation.Attempt, in.Validation.Error, in.Validation.Rounds)
		// The key identifies the latest attempt, including failure. A retained
		// successful value must not attribute a new draft's error to an old one.
		r.ValidationKey = in.ValidationKey
	}
	if !in.ScannedAt.IsZero() {
		r.ScannedAt = in.ScannedAt
	}
	d.Prune(now)
}
func latest(r Record) time.Time {
	at := r.TCP.Attempt
	for _, v := range []time.Time{r.Download.Attempt, r.Validation.Attempt, r.ScannedAt, r.Location.At} {
		if v.After(at) {
			at = v
		}
	}
	return at
}

// Prune retains at most four networks, 1000 records per network, for thirty days.
func (d *Document) Prune(now time.Time) {
	sort.SliceStable(d.Records, func(i, j int) bool { return latest(d.Records[i]).After(latest(d.Records[j])) })
	counts := map[string]int{}
	kept := make([]Record, 0, min(4000, len(d.Records)))
	for _, r := range d.Records {
		if latest(r).Before(now.Add(-30 * 24 * time.Hour)) {
			continue
		}
		if _, ok := counts[r.Network]; !ok && len(counts) >= 4 {
			continue
		}
		if counts[r.Network] >= 1000 {
			continue
		}
		counts[r.Network]++
		kept = append(kept, r)
	}
	d.Records = kept
}

// Decode refuses malformed or oversized evidence instead of silently overwriting it.
func Decode(data []byte) (Document, error) {
	d := NewDocument()
	if len(data) > 4*1024*1024 || json.Unmarshal(data, &d) != nil || d.Version != 1 || len(d.Records) > 4000 {
		return Document{}, errors.New("cf_cache_invalid")
	}
	if len(d.ScanNetwork) > 128 || (d.ScanNetwork == "") != d.ScanAt.IsZero() {
		return Document{}, errors.New("cf_cache_invalid")
	}
	seen := map[string]bool{}
	counts := map[string]int{}
	for _, r := range d.Records {
		// Explicit single-IP tests may add records for the current network beside the scan's own.
		if !IsAddress(r.IP) || len(r.Network) > 128 || r.Network == "" || seen[r.Network+"/"+r.IP] {
			return Document{}, errors.New("cf_cache_invalid")
		}
		seen[r.Network+"/"+r.IP] = true
		counts[r.Network]++
		if counts[r.Network] > 1000 || len(counts) > 4 || r.Prefix != "" && r.Prefix != Prefix(r.IP) || len(r.ValidationKey) > 64 {
			return Document{}, errors.New("cf_cache_invalid")
		}
		for _, field := range []string{r.Location.CountryCode, r.Location.Country, r.Location.Region, r.Location.City, r.Location.Source, r.Location.Error} {
			if len(field) > 128 || strings.IndexFunc(field, unicode.IsControl) >= 0 {
				return Document{}, errors.New("cf_cache_invalid")
			}
		}
		for _, s := range []Sample{r.TCP, r.Download, r.Validation} {
			if s.Value < 0 || math.IsNaN(s.Value) || math.IsInf(s.Value, 0) || len(s.Error) > 64 {
				return Document{}, errors.New("cf_cache_invalid")
			}
		}
	}
	return d, nil
}

// Encode validates the document before persistence or JSON export.
func Encode(d Document) ([]byte, error) {
	data, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	if _, err = Decode(data); err != nil {
		return nil, err
	}
	return data, nil
}

// Prefix identifies a display grouping, never evidence that an entire network is reachable.
func Prefix(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	bits := 24
	if a.Is6() {
		bits = 64
	}
	return netip.PrefixFrom(a, bits).Masked().String()
}
