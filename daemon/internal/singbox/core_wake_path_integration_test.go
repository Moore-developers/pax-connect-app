//go:build integration && with_clash_api

package singbox

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"pax-connect/daemon/internal/config"
)

// Real native lifecycle and mixed-egress verification, without a TUN, root,
// system proxy mutation, external network or saved user credentials.
func TestNativeWakeAcknowledgementDoesNotProveConnectivity(t *testing.T) {
	var available atomic.Bool
	available.Store(true)
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if available.Load() {
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer target.Close()
	port := freeLoopbackPort(t)
	profile := fmt.Sprintf(`{"log":{"disabled":true},"experimental":{"cache_file":{"path":%q}},"inbounds":[{"type":"mixed","tag":"mixed","listen":"127.0.0.1","listen_port":%d}],"outbounds":[{"type":"selector","tag":"proxy","outbounds":["fixture"],"default":"fixture"},{"type":"direct","tag":"fixture"}],"route":{"final":"proxy"}}`, filepath.Join(t.TempDir(), "cache.db"), port)
	runtime := NewCoreRuntime(context.Background())
	defer runtime.Close()
	if err := runtime.Apply(profile); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Clear() }()
	runtime.pathTarget = target.URL
	runtime.pathTLS = &tls.Config{RootCAs: target.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}
	instance := runtime.current().Instance()
	check := func(id string) config.CurrentPathResult {
		t.Helper()
		session, version := runtime.CurrentPathIdentity()
		run, err := runtime.PrepareCurrentPath(context.Background(), config.CurrentPathRequest{CheckID: id, ExpectedTag: "fixture", ExpectedSession: session, ExpectedVersion: version})
		if err != nil {
			t.Fatal(err)
		}
		return run(context.Background())
	}
	if result := check("before-sleep"); result.State != "passed" {
		t.Fatalf("baseline: %s/%s", result.State, result.Cause)
	}
	for _, online := range []bool{false, true} {
		available.Store(online)
		if err := runtime.SetPaused(true); err != nil {
			t.Fatal(err)
		}
		if err := runtime.SetPaused(false); err != nil {
			t.Fatal(err)
		}
		result := check(fmt.Sprintf("wake-%t", online))
		if (result.State == "passed") != online {
			t.Fatalf("wake ACK hid real target result: online=%t state=%s cause=%s", online, result.State, result.Cause)
		}
		if instance != runtime.current().Instance() {
			t.Fatal("wake verification reloaded the native instance")
		}
	}
}
