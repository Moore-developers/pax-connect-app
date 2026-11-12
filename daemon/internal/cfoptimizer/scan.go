package cfoptimizer

import (
	"context"
	"errors"
	"math/rand"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// Probe injects physical network I/O. It never reads or writes route configuration.
// ErrEnvironment suspends measurement without blaming individual IPs.
var ErrEnvironment = errors.New("cf_environment_unavailable")

type Probe struct {
	Locate   func(context.Context, string) (Location, error)
	Validate func(context.Context) error
	TCP      func(context.Context, string) (time.Duration, error)
	Download func(context.Context, string) (float64, error)
	Now      func() time.Time
}

// Progress is immutable stage progress, independent of UI/RPC types.
type Progress struct {
	Phase            string
	Completed, Total int
}

func (p Probe) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// DiscoveryTargets samples two addresses per /24 in the versioned CF IPv4 ranges.
func DiscoveryTargets() []string {
	out := []string{}
	for _, prefix := range networks {
		if !prefix.Addr().Is4() {
			continue
		}
		for a := prefix.Masked().Addr(); prefix.Contains(a); {
			out = append(out, a.Next().String())
			bytes := a.As4()
			bytes[3] = 128
			out = append(out, netip.AddrFrom4(bytes).String())
			next := a.As4()
			v := uint32(next[0])<<24 | uint32(next[1])<<16 | uint32(next[2])<<8 | uint32(next[3])
			v += 256
			a = netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), 0})
		}
	}
	return out
}

// MeasureTCP uses bounded workers; precise samples take the median of at least two successful rounds.
func MeasureTCP(ctx context.Context, ips []string, network string, rounds, parallel int, p Probe, emit func(Record), progress func(Progress)) error {
	if p.Validate != nil {
		if err := p.Validate(ctx); err != nil {
			return ErrEnvironment
		}
	}
	task, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan string)
	type observation struct {
		record Record
		fatal  bool
	}
	results := make(chan observation, max(1, parallel))
	var wg sync.WaitGroup
	for i := 0; i < min(parallel, len(ips)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ip := range jobs {
				good := []float64{}
				fatal := false
				for j := 0; j < rounds && task.Err() == nil; j++ {
					bounded, cancel := context.WithTimeout(task, 1200*time.Millisecond)
					value, err := p.TCP(bounded, ip)
					cancel()
					if errors.Is(err, ErrEnvironment) {
						fatal = true
						break
					}
					if err == nil && value > 0 {
						good = append(good, float64(value)/float64(time.Millisecond))
					}
				}
				if task.Err() != nil {
					continue
				}
				at := p.now()
				sample := Sample{Attempt: at, Rounds: len(good)}
				if len(good) > rounds/2 {
					sort.Float64s(good)
					sample.Value = median(good)
					sample.At = at
				} else {
					sample.Error = "cf_tcp_unreachable"
				}
				results <- observation{record: Record{IP: ip, Prefix: Prefix(ip), Network: network, TCP: sample, Reachable: sample.Error == ""}, fatal: fatal}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, ip := range ips {
			select {
			case jobs <- ip:
			case <-task.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()
	done := 0
	environmentFailed := false
	for r := range results {
		if r.fatal {
			environmentFailed = true
			cancel()
		} else if task.Err() == nil {
			// Publish each completed IP while peers are still running. A later
			// path failure invalidates the task, not already observed history.
			emit(r.record)
		}
		done++
		if done == len(ips) || done%max(1, parallel) == 0 {
			progress(Progress{Completed: done, Total: len(ips)})
		}
	}
	if environmentFailed {
		return ErrEnvironment
	}
	return ctx.Err()
}
func median(v []float64) float64 {
	middle := len(v) / 2
	if len(v)%2 == 0 {
		return (v[middle-1] + v[middle]) / 2
	}
	return v[middle]
}

// Scan ports bounded discovery/deep sampling from cf-ip-optimizer 1565f19.
// Opening/querying a workspace never invokes this function.
func Scan(ctx context.Context, network string, p Probe, random *rand.Rand, emit func(Record), progress func(Progress)) error {
	coarse := map[string]Record{}
	targets := DiscoveryTargets()
	err := MeasureTCP(ctx, targets, network, 1, 64, p, func(r Record) {
		if r.Reachable {
			coarse[r.Prefix] = best(coarse[r.Prefix], r)
		}
	}, func(v Progress) { v.Phase = "discovery"; progress(v) })
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	prefixes := []Record{}
	for _, r := range coarse {
		prefixes = append(prefixes, r)
	}
	sort.Slice(prefixes, func(i, j int) bool {
		if prefixes[i].TCP.Value == prefixes[j].TCP.Value {
			return prefixes[i].IP < prefixes[j].IP
		}
		return prefixes[i].TCP.Value < prefixes[j].TCP.Value
	})
	if len(prefixes) > 30 {
		prefixes = prefixes[:30]
	}
	ips := []string{}
	for _, seed := range prefixes {
		ips = append(ips, seed.IP)
		prefix := netip.MustParsePrefix(seed.Prefix)
		addresses := []string{}
		for a := prefix.Addr().Next(); prefix.Contains(a); a = a.Next() {
			if a.String() != seed.IP {
				addresses = append(addresses, a.String())
			}
		}
		random.Shuffle(len(addresses), func(i, j int) { addresses[i], addresses[j] = addresses[j], addresses[i] })
		count := min(40, len(addresses), 1200-len(ips))
		ips = append(ips, addresses[:count]...)
		if len(ips) >= 1200 {
			break
		}
	}
	err = MeasureTCP(ctx, ips, network, 3, 4, p, func(r Record) {
		if r.Reachable && r.TCP.Error == "" && r.TCP.Rounds >= 2 {
			r.ScannedAt = p.now()
			emit(r)
		}
	}, func(v Progress) { v.Phase = "precision"; progress(v) })
	return err
}
func best(previous, current Record) Record {
	if previous.IP == "" || current.TCP.Value < previous.TCP.Value {
		return current
	}
	return previous
}

// Download samples each candidate once with a bounded worker pool. Results are
// emitted on completion, independent of other candidates. Probe owns HTTP budget.
func Download(ctx context.Context, network string, ips []string, parallel int, p Probe, emit func(Record), progress func(Progress)) error {
	if p.Validate != nil {
		if err := p.Validate(ctx); err != nil {
			return ErrEnvironment
		}
	}
	if len(ips) == 0 || len(ips) > 20 {
		return errors.New("cf_candidate_limit")
	}
	parallel = min(8, max(1, parallel), len(ips))
	task, cancel := context.WithCancel(ctx)
	defer cancel()
	type outcome struct {
		record Record
		fatal  bool
	}
	jobs := make(chan string)
	results := make(chan outcome, parallel)
	var workers sync.WaitGroup
	for i := 0; i < parallel; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ip := range jobs {
				if task.Err() != nil {
					return
				}
				value, err := p.Download(task, ip)
				if errors.Is(err, ErrEnvironment) {
					// Deliver the environment failure even if its cancellation retires peers.
					results <- outcome{fatal: true}
					cancel()
					return
				}
				if task.Err() != nil {
					return
				}
				at := p.now()
				sample := Sample{Attempt: at}
				if err == nil && value > 0 {
					sample.Value = value
					sample.At = at
					sample.Rounds = 1
				} else {
					sample.Error = "cf_download_incomplete"
				}
				select {
				case results <- outcome{record: Record{IP: ip, Prefix: Prefix(ip), Network: network, Download: sample}}:
				case <-task.Done():
					return
				}
			}
		}()
	}
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		defer close(jobs)
		for _, ip := range ips {
			select {
			case jobs <- ip:
			case <-task.Done():
				return
			}
		}
	}()
	go func() { workers.Wait(); close(results) }()
	completed := 0
	environmentFailed := false
	for result := range results {
		if result.fatal {
			environmentFailed = true
			continue
		}
		if ctx.Err() != nil {
			continue
		}
		emit(result.record)
		completed++
		progress(Progress{Phase: "download", Completed: completed, Total: len(ips)})
	}
	<-producerDone
	if environmentFailed {
		return ErrEnvironment
	}
	return ctx.Err()
}
