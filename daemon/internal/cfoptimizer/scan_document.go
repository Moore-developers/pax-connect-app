package cfoptimizer

import (
	"math"
	"sort"
	"time"
)

// ScanDocument replaces every prior stage with fresh successful TCP observations.
// Selection is by latency, never completion time. Empty scans are durable too.
func ScanDocument(network string, records []Record, at time.Time) Document {
	byIP := map[string]Record{}
	for _, r := range records {
		if r.Network != network || !IsAddress(r.IP) || !r.Reachable || r.TCP.Error != "" || r.TCP.Rounds < 2 || r.TCP.Value <= 0 || math.IsNaN(r.TCP.Value) || math.IsInf(r.TCP.Value, 0) || r.TCP.At.IsZero() {
			continue
		}
		byIP[r.IP] = Record{IP: r.IP, Prefix: Prefix(r.IP), Network: network, TCP: r.TCP, Reachable: true, ScannedAt: r.ScannedAt, CheckedAt: r.TCP.Attempt}
	}
	out := Document{Version: 1, ScanNetwork: network, ScanAt: at, Records: []Record{}}
	for _, r := range byIP {
		out.Records = append(out.Records, r)
	}
	sort.Slice(out.Records, func(i, j int) bool {
		a, b := out.Records[i], out.Records[j]
		if a.TCP.Value == b.TCP.Value {
			return a.IP < b.IP
		}
		return a.TCP.Value < b.TCP.Value
	})
	if len(out.Records) > 1000 {
		out.Records = out.Records[:1000]
	}
	return out
}

// Accepts restricts later stage updates to the last completed scan's members.
// Legacy catalogs remain readable until their first replacement scan.
func (d Document) Accepts(ip, network string) bool {
	if d.ScanNetwork == "" {
		return true
	}
	if d.ScanNetwork != network {
		return false
	}
	for _, r := range d.Records {
		if r.IP == ip {
			return true
		}
	}
	return false
}
