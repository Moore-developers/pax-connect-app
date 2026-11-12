package stats

import (
	"errors"
	"fmt"
	"pax-connect/daemon/internal/config"
	"time"
)

type Bucket struct {
	RouteID          string
	At               time.Time
	Upload, Download uint64
	Gap              bool
}
type Day struct {
	RouteID, Date, Zone string
	Upload, Download    uint64
}
type Counter struct{ Upload, Download uint64 }
type Document struct {
	Version    int
	Revision   uint64
	Buckets    []Bucket
	Days       []Day
	Session    string
	Sequence   uint64
	Watermarks map[string]Counter
	Last       time.Time
	Clean      bool
	Partial    bool
}

func New() Document {
	return Document{Version: 1, Revision: 1, Watermarks: map[string]Counter{}, Buckets: []Bucket{}, Days: []Day{}}
}

// Accept differences monotonic native totals exactly once. Missing intervals are
// explicitly gaps; tests/CF instances never enter this product counter stream.
func (d *Document) Accept(v config.UsageSnapshot, tags map[string]string, now time.Time) {
	if v.Session == "" || v.Sequence == 0 {
		return
	}
	if d.Session == v.Session && v.Sequence <= d.Sequence {
		return
	}
	gap := v.Partial || (!d.Last.IsZero() && (v.At.Before(d.Last) || v.At.Sub(d.Last) > 3*time.Second))
	if d.Session != v.Session {
		d.Watermarks = map[string]Counter{}
		gap = true
	}
	if d.Watermarks == nil {
		d.Watermarks = map[string]Counter{}
	}
	for _, c := range v.Counters {
		id := tags[c.Tag]
		if id == "" {
			d.Partial = true
			continue
		}
		old := d.Watermarks[c.Tag]
		if c.Upload < old.Upload || c.Download < old.Download {
			gap = true
			old = Counter{}
		}
		up, down := c.Upload-old.Upload, c.Download-old.Download
		d.Watermarks[c.Tag] = Counter{c.Upload, c.Download}
		if up == 0 && down == 0 {
			continue
		}
		at := v.At.UTC().Truncate(5 * time.Minute)
		found := false
		for i := len(d.Buckets) - 1; i >= 0; i-- {
			b := &d.Buckets[i]
			if b.RouteID == id && b.At.Equal(at) {
				b.Upload += up
				b.Download += down
				b.Gap = b.Gap || gap
				found = true
				break
			}
		}
		if !found {
			d.Buckets = append(d.Buckets, Bucket{id, at, up, down, gap})
		}
		local := v.At.In(time.Local)
		date := local.Format("2006-01-02")
		zone := zoneIdentity(local)
		found = false
		for i := range d.Days {
			day := &d.Days[i]
			if day.RouteID == id && day.Date == date && day.Zone == zone {
				day.Upload += up
				day.Download += down
				found = true
				break
			}
		}
		if !found {
			d.Days = append(d.Days, Day{id, date, zone, up, down})
		}
	}
	cutoff := now.Add(-7 * 24 * time.Hour)
	buckets := d.Buckets[:0]
	for _, b := range d.Buckets {
		if b.At.After(cutoff) {
			buckets = append(buckets, b)
		}
	}
	d.Buckets = buckets
	if len(d.Buckets) > 100000 {
		d.Buckets = d.Buckets[len(d.Buckets)-100000:]
		d.Partial = true
	}
	days := d.Days[:0]
	for _, day := range d.Days {
		if day.Date >= cutoff.In(time.Local).Format("2006-01-02") {
			days = append(days, day)
		}
	}
	d.Days = days
	if len(d.Days) > 7000 {
		d.Days = d.Days[len(d.Days)-7000:]
		d.Partial = true
	}
	d.Session = v.Session
	d.Sequence = v.Sequence
	d.Last = v.At
	d.Clean = false
	d.Revision++
	d.Partial = d.Partial || gap
}

// zoneIdentity detects offset changes even when Go names the device zone "Local".
func zoneIdentity(at time.Time) string {
	name, offset := at.Zone()
	return fmt.Sprintf("%s/%s/%d", at.Location(), name, offset)
}
func (d Document) Today(id string, now time.Time) (uint64, uint64, bool) {
	var up, down uint64
	local := now.In(time.Local)
	date, zone := local.Format("2006-01-02"), zoneIdentity(local)
	partial, changed := d.Partial, false
	for _, day := range d.Days {
		if day.RouteID == id && day.Zone != zone {
			changed = true
		}
	}
	if changed {
		start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.Local)
		end := start.AddDate(0, 0, 1)
		// Rebuild only retained five-minute facts; missing/boundary coverage remains partial.
		for _, bucket := range d.Buckets {
			if bucket.RouteID == id && !bucket.At.Before(start) && bucket.At.Before(end) {
				up += bucket.Upload
				down += bucket.Download
			}
		}
		return up, down, true
	}
	for _, day := range d.Days {
		if day.RouteID == id && day.Date == date && day.Zone == zone {
			up += day.Upload
			down += day.Download
		}
	}
	return up, down, partial
}

// Validate checks the bounded, credential-free disk model before it becomes state.
func (d Document) Validate() error {
	if d.Version != 1 || d.Revision == 0 || len(d.Buckets) > 100000 || len(d.Days) > 7000 || len(d.Watermarks) > 10000 || len(d.Session) > 128 {
		return errors.New("usage_corrupt")
	}
	for _, b := range d.Buckets {
		if b.RouteID == "" || len(b.RouteID) > 128 || b.At.IsZero() || !b.At.Equal(b.At.UTC().Truncate(5*time.Minute)) {
			return errors.New("usage_corrupt")
		}
	}
	for _, day := range d.Days {
		if day.RouteID == "" || len(day.RouteID) > 128 || len(day.Zone) > 256 {
			return errors.New("usage_corrupt")
		}
		if _, err := time.Parse("2006-01-02", day.Date); err != nil {
			return errors.New("usage_corrupt")
		}
	}
	for tag := range d.Watermarks {
		if tag == "" || len(tag) > 128 {
			return errors.New("usage_corrupt")
		}
	}
	return nil
}
