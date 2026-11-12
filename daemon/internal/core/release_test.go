package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestClearCannotSucceedWithPaxSystemProxyStillEnabled(t *testing.T) {
	s := testServer(t, &fakeRuntime{state: "STARTED"})
	enabled := true
	s.releaseProxy = func(context.Context) error {
		if enabled {
			return errors.New("core_proxy_release_unconfirmed")
		}
		return nil
	}
	if _, err := s.ClearProfile(context.Background(), &emptypb.Empty{}); status.Code(err) != codes.Internal {
		t.Fatal("Clear acknowledged success while OS proxy is still enabled", err)
	}
	enabled = false
	if _, err := s.ClearProfile(context.Background(), &emptypb.Empty{}); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownDoesNotExitBeforeProxyReleaseIsConfirmed(t *testing.T) {
	s := testServer(t, &fakeRuntime{state: "STARTED"})
	s.releaseProxy = func(ctx context.Context) error {
		if ctx.Err() != nil {
			t.Fatal("client cancellation abandoned OS cleanup")
		}
		return errors.New("core_proxy_release_unconfirmed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Shutdown(ctx, &emptypb.Empty{}); status.Code(err) != codes.Internal {
		t.Fatal("shutdown acknowledged unconfirmed cleanup", err)
	}
	select {
	case <-s.done:
		t.Fatal("core stopped before confirmed cleanup")
	default:
	}
}

func TestReleaseClearsOnlyPaxAndWaitsForEffectiveState(t *testing.T) {
	enabled := true
	reads := 0
	writes := 0
	run := func(_ context.Context, name string, args ...string) (string, error) {
		if name == "/usr/sbin/scutil" {
			reads++
			if reads == 1 {
				return "<dictionary> {\n HTTPEnable : 1\n HTTPProxy : 127.0.0.1\n HTTPPort : 17890\n}", nil
			}
			return "<dictionary> {\n HTTPEnable : 1\n HTTPProxy : 127.0.0.1\n HTTPPort : 10808\n}", nil
		}
		if args[0] == "-listallnetworkservices" {
			return "Wi-Fi\nOther\n", nil
		}
		if strings.HasPrefix(args[0], "-get") {
			if args[1] == "Other" {
				return "Enabled: Yes\nServer: 127.0.0.1\nPort: 10808\n", nil
			}
			if enabled {
				return "Enabled: Yes\nServer: 127.0.0.1\nPort: 17890\n", nil
			}
			return "Enabled: No\n", nil
		}
		if args[1] != "Wi-Fi" {
			t.Fatal("changed third-party proxy")
		}
		writes++
		// All three protocols still have the same fixture state until cleared.
		if writes == 3 {
			enabled = false
		}
		return "", nil
	}
	ctx, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if err := releaseProxy(ctx, 17890, run); err != nil {
		t.Fatal(err)
	}
	if writes != 3 || reads != 2 {
		t.Fatalf("writes=%d readback=%d", writes, reads)
	}
}

func TestReleaseFailsWhenEffectivePaxProxyStaysEnabled(t *testing.T) {
	for _, raw := range []string{"malformed", "<dictionary> {\n HTTPEnable : 1\n HTTPProxy : 127.0.0.1\n HTTPPort : 17890\n}"} {
		t.Run(fmt.Sprint(len(raw)), func(t *testing.T) {
			ctx, done := context.WithTimeout(context.Background(), time.Millisecond)
			defer done()
			err := releaseProxy(ctx, 17890, func(_ context.Context, name string, _ ...string) (string, error) {
				if name == "/usr/sbin/scutil" {
					return raw, nil
				}
				return "", nil
			})
			if err == nil {
				t.Fatal("unconfirmed release succeeded")
			}
		})
	}
}

func TestScopedProxyCannotHidePaxProxy(t *testing.T) {
	for _, raw := range []string{
		"<dictionary> {\n HTTPEnable : 1\n HTTPProxy : 127.0.0.1\n HTTPPort : 17890\n __SCOPED__ : <dictionary> {\n en9 : <dictionary> {\n HTTPEnable : 1\n HTTPProxy : 127.0.0.1\n HTTPPort : 10808\n }\n }\n}",
		"<dictionary> {\n HTTPEnable : 1\n HTTPProxy : 127.0.0.1\n HTTPPort : 10808\n __SCOPED__ : <dictionary> {\n en9 : <dictionary> {\n HTTPEnable : 1\n HTTPProxy : 127.0.0.1\n HTTPPort : 17890\n }\n }\n}",
	} {
		ctx, done := context.WithTimeout(context.Background(), time.Millisecond)
		err := releaseProxy(ctx, 17890, func(_ context.Context, name string, _ ...string) (string, error) {
			if name == "/usr/sbin/scutil" {
				return raw, nil
			}
			return "", nil
		})
		done()
		if err == nil {
			t.Fatal("nested dictionary hid an enabled Pax proxy")
		}
	}
}

func TestEffectiveProxyDictionaryValidation(t *testing.T) {
	for _, tt := range []struct {
		name, raw       string
		active, invalid bool
	}{
		{"empty", "<dictionary> {\n}", false, false},
		{"exceptions and third party scope", "<dictionary> {\n ExceptionsList : <array> {\n 0 : localhost\n }\n __SCOPED__ : <dictionary> {\n en0 : <dictionary> {\n HTTPEnable : 1\n HTTPProxy : 127.0.0.1\n HTTPPort : 10808\n }\n }\n}", false, false},
		{"unclosed", "<dictionary> {\n HTTPEnable : 0", false, true},
		{"duplicate", "<dictionary> {\n HTTPEnable : 1\n HTTPEnable : 0\n}", false, true},
		{"missing endpoint", "<dictionary> {\n HTTPEnable : 1\n}", false, true},
		{"trailing", "<dictionary> {\n}\nHTTPEnable : 0", false, true},
		{"invalid flag", "<dictionary> {\n HTTPEnable : unknown\n}", false, true},
		{"invalid port", "<dictionary> {\n HTTPEnable : 1\n HTTPProxy : 127.0.0.1\n HTTPPort : 99999\n}", false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			active, err := effectivePaxProxy(tt.raw, 17890)
			if (err != nil) != tt.invalid || active != tt.active {
				t.Fatalf("active=%v invalid=%v", active, err != nil)
			}
		})
	}
}
