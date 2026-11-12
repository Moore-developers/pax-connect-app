//go:build hostmeasurementanalysis

package singbox

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	"golang.org/x/sys/unix"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"sort"
	"sync"
	"syscall"
	"testing"
	"time"
)

func analysisProfiles(t *testing.T) []config.RouteConfig {
	dir := os.Getenv("PAX_COMPARE_DIRECTORY")
	if dir == "" {
		t.Skip("explicit private directory required")
	}
	cat, exists, err := config.NewCatalogStore(filepath.Join(dir, "routes.json"), uint32(os.Getuid())).Load()
	if err != nil || !exists {
		t.Fatal("private catalog unavailable")
	}
	policies := config.NewTLSPolicyStore(filepath.Join(dir, "tls-policies.json"), uint32(os.Getuid()))
	profiles := []config.RouteConfig{}
	for _, kind := range []string{"selected", "cf", "anytls", "anytls2"} {
		for _, r := range cat.Routes {
			match := (kind == "selected" && r.Config.ID == cat.SelectedID) || (kind == "cf" && r.Config.ProtocolName == "vmess" && r.Config.Transport == "ws") || ((kind == "anytls" || kind == "anytls2") && r.Config.ProtocolName == "anytls")
			if !match || r.Hidden {
				continue
			}
			duplicate := false
			for _, p := range profiles {
				if p.ID == r.Config.ID {
					duplicate = true
				}
			}
			if duplicate {
				continue
			}
			p := r.Config
			view, err := policies.Lookup(p)
			if err != nil || view.NeedsConfirmation {
				continue
			}
			p.TLSResolved = view
			profiles = append(profiles, p)
			break
		}
	}
	return profiles
}
func analysisBound(p config.RouteConfig, iface string) config.RouteConfig {
	var object map[string]any
	_ = json.Unmarshal(config.DocumentForRoute(p), &object)
	object["bind_interface"] = iface
	p.Document, _ = json.Marshal(object)
	return p
}
func analysisLog(t *testing.T, v any) { b, _ := json.Marshal(v); t.Log(string(b)) }
func TestHostLatencyComparison(t *testing.T) {
	profiles := analysisProfiles(t)
	iface := os.Getenv("PAX_COMPARE_INTERFACE")
	if iface == "" {
		t.Fatal("physical interface required")
	}
	physical, err := net.InterfaceByName(iface)
	if err != nil {
		t.Fatal("interface unavailable")
	}
	runtime := NewMeasurements(measurement.NewExitLookup())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	for index, p := range profiles[:min(3, len(profiles))] {
		for attempt := 0; attempt < 3; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			dialer := net.Dialer{Control: func(network, address string, c syscall.RawConn) error {
				var failure error
				controlErr := c.Control(func(fd uintptr) {
					level, option := unix.IPPROTO_IP, unix.IP_BOUND_IF
					if network == "tcp6" {
						level, option = unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF
					}
					failure = unix.SetsockoptInt(int(fd), level, option, physical.Index)
				})
				if controlErr != nil {
					return controlErr
				}
				return failure
			}}
			start := time.Now()
			conn, e := dialer.DialContext(ctx, "tcp", net.JoinHostPort(p.Server, fmt.Sprint(p.Port)))
			elapsed := time.Since(start)
			if conn != nil {
				_ = conn.Close()
			}
			cancel()
			analysisLog(t, map[string]any{"kind": "tcp_physical", "profile": index, "attempt": attempt, "ok": e == nil, "ms": elapsed.Milliseconds()})
		}
		for _, binding := range []string{"default", "physical"} {
			if binding == "default" && index > 0 {
				continue
			}
			profile := p
			if binding == "physical" {
				profile = analysisBound(p, iface)
			}
			for targetIndex, target := range []string{"https://1.1.1.1/", "https://www.google.com/generate_204", "https://www.gstatic.com/generate_204"} {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				e := runtime.withMeasurementOutbound(ctx, profile, func(ctx context.Context, outbound adapter.Outbound) error {
					var dialMS int64
					transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 4 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
						start := time.Now()
						c, e := outbound.DialContext(ctx, network, M.ParseSocksaddr(address))
						dialMS = time.Since(start).Milliseconds()
						if e != nil {
							return nil, nodeMeasurementFailure(e)
						}
						return c, nil
					}}
					defer transport.CloseIdleConnections()
					client := &http.Client{Transport: transport, Timeout: 7 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
					for attempt := 0; attempt < 2; attempt++ {
						tlsStart := time.Time{}
						tlsMS := int64(0)
						reused := false
						dialMS = 0
						trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }, TLSHandshakeStart: func() { tlsStart = time.Now() }, TLSHandshakeDone: func(_ tls.ConnectionState, _ error) { tlsMS = time.Since(tlsStart).Milliseconds() }}
						req, _ := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, target, nil)
						start := time.Now()
						response, e := client.Do(req)
						headers := time.Since(start)
						status := 0
						code := ""
						if e == nil {
							status = response.StatusCode
							_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 128*1024))
							_ = response.Body.Close()
						} else {
							code = measurement.Classify(e, false).Code
						}
						analysisLog(t, map[string]any{"kind": "http", "profile": index, "binding": binding, "target": targetIndex, "attempt": attempt, "ok": e == nil, "status": status, "ms": headers.Milliseconds(), "node_dial_ms": dialMS, "target_tls_ms": tlsMS, "reused": reused, "code": code})
						if e != nil {
							break
						}
						time.Sleep(100 * time.Millisecond)
					}
					return nil
				})
				cancel()
				if e != nil {
					analysisLog(t, map[string]any{"kind": "runtime", "profile": index, "code": measurement.Classify(e, true).Code})
				}
			}
		}
	}
}
func TestHostDownloadParallelComparison(t *testing.T) {
	if os.Getenv("PAX_COMPARE_DOWNLOAD") != "1" {
		t.Skip("explicit download budget required")
	}
	profiles := analysisProfiles(t)
	iface := os.Getenv("PAX_COMPARE_INTERFACE")
	for i := range profiles {
		profiles[i] = analysisBound(profiles[i], iface)
	}
	runtime := NewMeasurements(measurement.NewExitLookup())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	// 4 routes x 8 MiB x 3 settings x 2 rounds = at most 192 MiB payload.
	profiles = profiles[:min(4, len(profiles))]
	for round := 0; round < 2; round++ {
		for _, parallel := range []int{1, 2, 4} {
			start := time.Now()
			jobs := make(chan int, len(profiles))
			for i := range profiles {
				jobs <- i
			}
			close(jobs)
			var done sync.WaitGroup
			var mu sync.Mutex
			success := 0
			bytes := uint64(0)
			samples := []int64{}
			for worker := 0; worker < parallel; worker++ {
				done.Add(1)
				go func() {
					defer done.Done()
					for i := range jobs {
						ctx, cancel := context.WithTimeout(context.Background(), measurement.DownloadTimeout)
						value, e := runtime.DownloadRoute(ctx, profiles[i])
						cancel()
						mu.Lock()
						if e == nil {
							success++
							bytes += value.Bytes
							samples = append(samples, int64(value.BytesPerSecond))
						}
						mu.Unlock()
					}
				}()
			}
			done.Wait()
			elapsed := time.Since(start)
			analysisLog(t, map[string]any{"kind": "download_parallel", "round": round, "parallel": parallel, "targets": len(profiles), "success": success, "elapsed_ms": elapsed.Milliseconds(), "successful_bytes": bytes, "successful_Bps": float64(bytes) / elapsed.Seconds(), "per_route_Bps": samples})
		}
	}
}

func TestHostPhysicalEgressObservation(t *testing.T) {
	if os.Getenv("PAX_COMPARE_DIRECTORY") == "" {
		t.Skip("explicit host analysis only")
	}
	iface, err := net.InterfaceByName(os.Getenv("PAX_COMPARE_INTERFACE"))
	if err != nil || iface.Flags&net.FlagLoopback != 0 {
		t.Fatal("physical interface unavailable")
	}
	dialer := net.Dialer{Timeout: 4 * time.Second, Control: func(network, address string, c syscall.RawConn) error {
		var failure error
		controlErr := c.Control(func(fd uintptr) {
			level, opt := unix.IPPROTO_IP, unix.IP_BOUND_IF
			if network == "tcp6" {
				level, opt = unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF
			}
			failure = unix.SetsockoptInt(int(fd), level, opt, iface.Index)
		})
		if controlErr != nil {
			return controlErr
		}
		return failure
	}}
	transport := &http.Transport{Proxy: nil, DialContext: dialer.DialContext, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	start := time.Now()
	ip, code := measurement.Exit(ctx, client)
	analysisLog(t, map[string]any{"kind": "physical_egress", "ok": code == "", "code": code, "has_public_ip": ip != "", "elapsed_ms": time.Since(start).Milliseconds(), "bound_interface": iface.Name})
}

func analysisEightProfiles(t *testing.T) []config.RouteConfig {
	profiles := analysisProfiles(t)
	dir := os.Getenv("PAX_COMPARE_DIRECTORY")
	catalog, exists, err := config.NewCatalogStore(filepath.Join(dir, "routes.json"), uint32(os.Getuid())).Load()
	if err != nil || !exists {
		t.Fatal("private catalog unavailable")
	}
	saved, err := config.NewMeasurementStore(filepath.Join(dir, "route-measurements.json"), uint32(os.Getuid())).Load()
	if err != nil {
		t.Fatal("private measurement unavailable")
	}
	policies := config.NewTLSPolicyStore(filepath.Join(dir, "tls-policies.json"), uint32(os.Getuid()))
	// Prefer distinct physical endpoints, then distinct saved profiles; identity
	// values and credentials are never written to output.
	for _, distinct := range []bool{true, false} {
		for _, record := range catalog.Routes {
			if len(profiles) >= 8 {
				break
			}
			sample := saved.Records[record.Config.ID].Latency
			if record.Hidden || sample == nil || !sample.Success {
				continue
			}
			duplicate := false
			for _, p := range profiles {
				if p.ID == record.Config.ID || (distinct && p.Server == record.Config.Server && p.Port == record.Config.Port) {
					duplicate = true
				}
			}
			if duplicate {
				continue
			}
			profile := record.Config
			view, e := policies.Lookup(profile)
			if e != nil || view.NeedsConfirmation {
				continue
			}
			profile.TLSResolved = view
			profiles = append(profiles, profile)
		}
	}
	if len(profiles) != 8 {
		t.Fatal("eight distinct saved profiles required")
	}
	return profiles
}

func TestHostWarmLatencyFiveRounds(t *testing.T) {
	profiles := analysisProfiles(t)
	iface := os.Getenv("PAX_COMPARE_INTERFACE")
	runtime := NewMeasurements(measurement.NewExitLookup())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	for round := 0; round < 5; round++ {
		for index, profile := range profiles[:min(3, len(profiles))] {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			e := runtime.withMeasurementOutbound(ctx, analysisBound(profile, iface), func(ctx context.Context, outbound adapter.Outbound) error {
				transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 3 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
					c, e := outbound.DialContext(ctx, network, M.ParseSocksaddr(address))
					if e != nil {
						return nil, nodeMeasurementFailure(e)
					}
					return c, nil
				}}
				defer transport.CloseIdleConnections()
				client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
				for attempt := 0; attempt < 2; attempt++ {
					reused := false
					trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}
					req, _ := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, "https://www.google.com/generate_204", nil)
					start := time.Now()
					response, err := client.Do(req)
					elapsed := time.Since(start)
					status := 0
					code := ""
					if err == nil {
						status = response.StatusCode
						_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 16384))
						_ = response.Body.Close()
					} else {
						code = measurement.Classify(err, false).Code
					}
					analysisLog(t, map[string]any{"kind": "warm_five_rounds", "round": round, "profile": index, "attempt": attempt, "ms": elapsed.Milliseconds(), "reused": reused, "status": status, "code": code})
					if err != nil {
						break
					}
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(100 * time.Millisecond):
					}
				}
				return nil
			})
			cancel()
			if e != nil {
				analysisLog(t, map[string]any{"kind": "warm_runtime", "round": round, "profile": index, "code": measurement.Classify(e, true).Code})
			}
		}
	}
}

type analysisDownloadSample struct {
	Profile   int     `json:"profile"`
	Bytes     int64   `json:"bytes"`
	ActiveMS  int64   `json:"active_ms"`
	ElapsedMS int64   `json:"elapsed_ms"`
	Valid     bool    `json:"valid"`
	End       string  `json:"end"`
	Bps       float64 `json:"bps"`
}

// This wrapper observes partial bytes through the existing measurement module.
// Only the test client adds the proposed body-window deadline; production stays unchanged.
type analysisCountedBody struct {
	io.ReadCloser
	sample *analysisDownloadSample
	first  time.Time
}

func (b *analysisCountedBody) Read(p []byte) (int, error) {
	n, e := b.ReadCloser.Read(p)
	if n > 0 {
		if b.first.IsZero() {
			b.first = time.Now()
		}
		b.sample.Bytes += int64(n)
	}
	return n, e
}

type analysisWindowClient struct {
	client       *http.Client
	sample       *analysisDownloadSample
	body         *analysisCountedBody
	bodyDeadline time.Time
	stop         context.CancelFunc
	timer        *time.Timer
}

func (c *analysisWindowClient) Do(req *http.Request) (*http.Response, error) {
	child, cancel := context.WithCancel(req.Context())
	c.stop = cancel
	response, err := c.client.Do(req.Clone(child))
	if err != nil {
		return nil, err
	}
	if response.StatusCode == 200 {
		c.bodyDeadline = time.Now().Add(6 * time.Second)
		c.timer = time.AfterFunc(6*time.Second, cancel)
		c.body = &analysisCountedBody{ReadCloser: response.Body, sample: c.sample}
		response.Body = c.body
	}
	return response, nil
}
func TestHostEightParallelWindow(t *testing.T) {
	if os.Getenv("PAX_COMPARE_DOWNLOAD") != "8" {
		t.Skip("explicit 384 MiB maximum budget required")
	}
	profiles := analysisEightProfiles(t)
	iface := os.Getenv("PAX_COMPARE_INTERFACE")
	runtime := NewMeasurements(measurement.NewExitLookup())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	// Two rotated rounds of 2/4/8 workers, eight unique routes, at most
	// 8*8MiB*3*2=384MiB payload. No retry and no TUN changes.
	for round := 0; round < 2; round++ {
		order := []int{2, 4, 8}
		if round == 1 {
			order = []int{8, 4, 2}
		}
		for _, parallel := range order {
			start := time.Now()
			jobs := make(chan int, 8)
			for i := 0; i < 8; i++ {
				jobs <- (i + round*3) % 8
			}
			close(jobs)
			var done sync.WaitGroup
			var mu sync.Mutex
			samples := []analysisDownloadSample{}
			for worker := 0; worker < parallel; worker++ {
				done.Add(1)
				go func() {
					defer done.Done()
					for index := range jobs {
						sample := analysisDownloadSample{Profile: index}
						begin := time.Now()
						ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
						err := runtime.withMeasurementOutbound(ctx, analysisBound(profiles[index], iface), func(ctx context.Context, outbound adapter.Outbound) error {
							transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
								conn, e := outbound.DialContext(ctx, network, M.ParseSocksaddr(address))
								if e != nil {
									return nil, nodeMeasurementFailure(e)
								}
								return conn, nil
							}}
							defer transport.CloseIdleConnections()
							client := &analysisWindowClient{client: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, sample: &sample}
							_, e := measurement.Download(ctx, client, measurement.DownloadBudget)
							if client.timer != nil {
								client.timer.Stop()
							}
							if client.stop != nil {
								client.stop()
							}
							if client.body != nil && !client.body.first.IsZero() {
								sample.ActiveMS = time.Since(client.body.first).Milliseconds()
							}
							switch {
							case e == nil:
								sample.End = "byte_cap"
							case ctx.Err() != nil:
								sample.End = "total_timeout"
							case !client.bodyDeadline.IsZero() && !time.Now().Before(client.bodyDeadline):
								sample.End = "sample_window"
							default:
								var failure *measurement.Failure
								if errors.As(e, &failure) {
									sample.End = failure.Code
								} else {
									sample.End = "transfer_failed"
								}
							}
							sample.Valid = (sample.End == "byte_cap" || sample.End == "sample_window") && sample.Bytes >= 256*1024 && sample.ActiveMS >= 1000
							if sample.ActiveMS > 0 {
								sample.Bps = float64(sample.Bytes) / float64(sample.ActiveMS) * 1000
							}
							return e
						})
						_ = err
						sample.ElapsedMS = time.Since(begin).Milliseconds()
						if sample.End == "" {
							sample.End = "runtime_failed"
						}
						cancel()
						mu.Lock()
						samples = append(samples, sample)
						mu.Unlock()
					}
				}()
			}
			done.Wait()
			elapsed := time.Since(start)
			sort.Slice(samples, func(i, j int) bool { return samples[i].Profile < samples[j].Profile })
			bytes, valid := int64(0), 0
			for _, s := range samples {
				bytes += s.Bytes
				if s.Valid {
					valid++
				}
			}
			analysisLog(t, map[string]any{"kind": "download_window8", "round": round, "parallel": parallel, "elapsed_ms": elapsed.Milliseconds(), "valid": valid, "payload_bytes_all_results": bytes, "aggregate_Bps": float64(bytes) / elapsed.Seconds(), "samples": samples})
		}
	}
}
