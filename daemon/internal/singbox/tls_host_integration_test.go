//go:build hosttls && with_quic

package singbox

import (
	"context"
	"errors"
	"os"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"testing"
	"time"
)

func TestExistingNodeExplicitTLSPolicies(t *testing.T) {
	path := os.Getenv("PAX_TLS_AUDIT_PROFILE")
	if path == "" {
		t.Skip("explicit private fixture required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("fixture read")
	}
	p, err := config.ParseDebugProfile(data)
	if err != nil {
		t.Fatal("fixture invalid")
	}
	p.TLSResolved = config.TLSPolicyView{Mode: config.TLSStrict, IdentityToken: config.TLSIdentity(p)}
	runtime := NewMeasurements(measurement.NewExitLookup())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	_, err = runtime.TestRoute(ctx, p)
	cancel()
	var certificate *CertificateError
	if !errors.As(err, &certificate) {
		t.Fatalf("strict did not report node certificate failure (classified=%t)", classifyCertificate(err) != nil)
	}
	t.Log("strict rejected server certificate with sanitized reason:", certificate.Reason)
	p.TLSResolved.Mode = config.TLSSkip
	ctx, cancel = context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	duration, err := runtime.TestRoute(ctx, p)
	if err != nil {
		t.Fatal("explicit skip probe failed")
	}
	t.Logf("explicit skip latency success; duration=%s; identity remains unverified", duration)
}
