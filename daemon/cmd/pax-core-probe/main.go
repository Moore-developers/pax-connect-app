package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
	pb "pax-connect/daemon/gen/proto/corev1"
)

type observation struct {
	TUN        string `json:"tun,omitempty"`
	TUNPresent bool   `json:"tun_present"`
	Routes     int    `json:"routes"`
	ProxyOn    bool   `json:"proxy_on"`
	ProxyOff   bool   `json:"proxy_off"`
}
type transition struct {
	RPCMS   float64     `json:"rpc_ms"`
	TUNMS   *float64    `json:"tun_ms"`
	RouteMS *float64    `json:"route_ms"`
	ProxyMS *float64    `json:"proxy_ms"`
	Final   observation `json:"final"`
}
type cycle struct {
	Number int        `json:"number"`
	On     transition `json:"on"`
	Off    transition `json:"off"`
}
type result struct {
	Started  string  `json:"started"`
	Cycles   []cycle `json:"cycles"`
	Shutdown bool    `json:"shutdown"`
	Clean    bool    `json:"clean"`
	Failure  string  `json:"failure,omitempty"`
}

func millis(t time.Time) float64 { return float64(time.Since(t).Microseconds()) / 1000 }
func command(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, name, args...).Output()
	return string(b), err
}
func values(raw string) map[string]string {
	v := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		k, s, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok {
			v[strings.TrimSpace(k)] = strings.TrimSpace(s)
		}
	}
	return v
}
func ownTUN() (string, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, i := range ifs {
		if !strings.HasPrefix(i.Name, "utun") {
			continue
		}
		addrs, err := i.Addrs()
		if err != nil {
			return "", err
		}
		for _, a := range addrs {
			ip, _, err := net.ParseCIDR(a.String())
			if err == nil && ip.String() == "172.19.77.1" {
				return i.Name, nil
			}
		}
	}
	return "", nil
}
func baseline(ctx context.Context) error {
	ifs, err := net.Interfaces()
	if err != nil {
		return err
	}
	for _, i := range ifs {
		if !strings.HasPrefix(i.Name, "utun") {
			continue
		}
		addrs, err := i.Addrs()
		if err != nil {
			return err
		}
		for _, a := range addrs {
			ip, _, err := net.ParseCIDR(a.String())
			if err == nil && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() {
				return errors.New("precondition: third-party TUN has an address")
			}
		}
	}
	raw, err := command(ctx, "/usr/sbin/networksetup", "-listallnetworkservices")
	if err != nil {
		return errors.New("precondition: cannot list proxy services")
	}
	for _, service := range strings.Split(raw, "\n") {
		service = strings.TrimSpace(service)
		if service == "" || strings.HasPrefix(service, "An asterisk") {
			continue
		}
		service = strings.TrimPrefix(service, "*")
		for _, kind := range []string{"webproxy", "securewebproxy", "socksfirewallproxy", "autoproxyurl", "proxyautodiscovery"} {
			raw, err = command(ctx, "/usr/sbin/networksetup", "-get"+kind, service)
			if err != nil {
				return errors.New("precondition: cannot read proxy state")
			}
			if values(raw)["Enabled"] == "Yes" || strings.Contains(raw, "Auto Proxy Discovery: On") {
				return errors.New("precondition: system proxy is enabled")
			}
		}
	}
	return nil
}
func observe(ctx context.Context, tun string, port int) (observation, error) {
	now, err := ownTUN()
	if err != nil {
		return observation{}, err
	}
	if tun == "" {
		tun = now
	}
	o := observation{TUN: tun, TUNPresent: now != ""}
	if tun != "" {
		for _, family := range []string{"inet", "inet6"} {
			raw, err := command(ctx, "/usr/sbin/netstat", "-rn", "-f", family)
			if err != nil {
				return o, errors.New("route observation failed")
			}
			for _, line := range strings.Split(raw, "\n") {
				f := strings.Fields(line)
				if len(f) >= 4 && f[3] == tun {
					o.Routes++
				}
			}
		}
	}
	raw, err := command(ctx, "/usr/sbin/scutil", "--proxy")
	if err != nil {
		return o, errors.New("proxy observation failed")
	}
	v := values(raw)
	o.ProxyOff = true
	o.ProxyOn = true
	for _, kind := range []string{"HTTP", "HTTPS", "SOCKS"} {
		enabled := v[kind+"Enable"] == "1"
		o.ProxyOff = o.ProxyOff && !enabled
		o.ProxyOn = o.ProxyOn && enabled && v[kind+"Proxy"] == "127.0.0.1" && v[kind+"Port"] == strconv.Itoa(port)
	}
	return o, nil
}
func measure(ctx context.Context, tun string, port int, on bool, rpc func() error) (transition, error) {
	start := time.Now()
	t := transition{}
	// Run RPC beside observation so timings include transitions during reload.
	type rpcResult struct {
		err error
		ms  float64
	}
	done := make(chan rpcResult, 1)
	go func() { err := rpc(); done <- rpcResult{err, millis(start)} }()
	rpcDone := false
	for {
		o, err := observe(ctx, tun, port)
		if err != nil {
			return t, err
		}
		if tun == "" {
			tun = o.TUN
		}
		t.Final = o
		record := func(p **float64, ok bool) {
			if *p == nil && ok {
				v := millis(start)
				*p = &v
			}
		}
		record(&t.TUNMS, o.TUNPresent == on)
		record(&t.RouteMS, on && o.Routes > 0 || !on && o.Routes == 0)
		record(&t.ProxyMS, on && o.ProxyOn || !on && o.ProxyOff)
		if !rpcDone {
			select {
			case completed := <-done:
				err = completed.err
				t.RPCMS = completed.ms
				rpcDone = true
				if err != nil {
					return t, errors.New("profile RPC failed")
				}
			default:
			}
		}
		if rpcDone && t.TUNMS != nil && t.RouteMS != nil && t.ProxyMS != nil {
			return t, nil
		}
		select {
		case <-ctx.Done():
			return t, errors.New("transition deadline exceeded")
		case <-time.After(50 * time.Millisecond):
		}
	}
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	socket := flag.String("socket", "/private/var/run/pax-core/core.sock", "core Unix socket")
	out := flag.String("out", "tests/proxy-core/results.json", "JSON result path")
	port := flag.Int("mixed-port", 17893, "loopback mixed port")
	timeout := flag.Duration("timeout", 90*time.Second, "overall deadline")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	ctx, deadline := context.WithTimeout(ctx, *timeout)
	defer deadline()
	res := result{Started: time.Now().UTC().Format(time.RFC3339)}
	flush := func() error {
		b, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(*out, append(b, '\n'), 0600)
	}
	if !filepath.IsAbs(*socket) || *port < 1 || *port > 65535 {
		return errors.New("invalid flags")
	}
	if err := flush(); err != nil {
		return err
	}
	if err := baseline(ctx); err != nil {
		res.Failure = err.Error()
		_ = flush()
		return err
	}
	conn, err := grpc.NewClient("unix://"+*socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	client := pb.NewPaxCoreClient(conn)
	empty := &emptypb.Empty{}
	defer func() {
		if !res.Shutdown {
			cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
			defer stop()
			_, e := client.ClearProfile(cleanup, empty)
			if e != nil {
				res.Failure += "; emergency ClearProfile failed"
			}
			_ = flush()
		}
	}()
	profile := fmt.Sprintf(`{"inbounds":[{"type":"tun","tag":"tun-in","address":["172.19.77.1/30"],"mtu":1500,"auto_route":true,"stack":"system"},{"type":"mixed","tag":"mixed-in","listen":"127.0.0.1","listen_port":%d,"set_system_proxy":true}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"auto_detect_interface":true,"final":"direct"}}`, *port)
	for n := 1; n <= 3; n++ {
		res.Cycles = append(res.Cycles, cycle{Number: n})
		c := &res.Cycles[len(res.Cycles)-1]
		step, stop := context.WithTimeout(ctx, 15*time.Second)
		c.On, err = measure(step, "", *port, true, func() error {
			_, e := client.ApplyProfile(step, &pb.ApplyProfileRequest{ProfileJson: profile, Revision: uint64(n)})
			return e
		})
		stop()
		if err != nil {
			res.Failure = err.Error()
			_ = flush()
			return err
		}
		if err = flush(); err != nil {
			return err
		}
		step, stop = context.WithTimeout(ctx, 15*time.Second)
		c.Off, err = measure(step, c.On.Final.TUN, *port, false, func() error { _, e := client.ClearProfile(step, empty); return e })
		stop()
		if err != nil {
			res.Failure = err.Error()
			_ = flush()
			return err
		}
		if err = flush(); err != nil {
			return err
		}
	}
	if _, err = client.Shutdown(ctx, empty); err != nil {
		res.Failure = "shutdown RPC failed"
		_ = flush()
		return errors.New(res.Failure)
	}
	res.Shutdown = true
	res.Clean = baseline(ctx) == nil
	if !res.Clean {
		res.Failure = "final baseline is not clean"
	}
	if err = flush(); err != nil {
		return err
	}
	if !res.Clean {
		return errors.New(res.Failure)
	}
	fmt.Println("3 cycles recorded; Shutdown acknowledged; clean baseline observed")
	return nil
}
