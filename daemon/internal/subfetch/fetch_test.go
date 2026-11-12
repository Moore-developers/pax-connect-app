package subfetch

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func lookupAll(addresses ...string) func(context.Context, string) ([]netip.Addr, error) {
	return func(context.Context, string) ([]netip.Addr, error) {
		var parsed []netip.Addr
		for _, address := range addresses {
			parsed = append(parsed, netip.MustParseAddr(address))
		}
		return parsed, nil
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}

const publicAddress = "93.184.216.34"

func TestPrivateAndSpecialTargetsAreRejectedBeforeConnecting(t *testing.T) {
	reached := false
	transport := roundTrip(func(*http.Request) (*http.Response, error) {
		reached = true
		return response(200, "vless://node"), nil
	})
	// 任何域名都解析到本机，覆盖「域名不是字面量」那条路径。
	local := func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	for _, target := range []string{
		"https://127.0.0.1/sub", "https://localhost:8443/sub", "https://[::1]/sub",
		"https://10.0.0.5/sub", "https://192.168.1.10/sub", "https://172.16.4.4/sub",
		"https://169.254.169.254/latest/meta-data", "https://0.0.0.0/sub",
		"https://100.64.3.9/sub", "https://198.18.0.1/sub", "https://[fe80::1]/sub",
		"https://[fd00::1]/sub", "https://[2001:db8::1]/sub", "https://224.0.0.1/sub",
		"https://internal.example/sub",
	} {
		fetcher := NewWithOptions(Options{Lookup: local, Transport: transport})
		if _, err := fetcher.FetchSubscription(context.Background(), target); !errors.Is(err, ErrTarget) {
			t.Fatalf("%s: want ErrTarget, got %v", target, err)
		}
	}
	if reached {
		t.Fatal("private target was dialed")
	}
}

func TestNonHTTPSSchemeAndRedirectLimitAreRejected(t *testing.T) {
	fetcher := NewWithOptions(Options{Lookup: lookupAll(publicAddress), Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return response(200, "x"), nil
	})})
	for _, target := range []string{"http://example.com/sub", "file:///etc/passwd", "ftp://example.com/sub"} {
		if _, err := fetcher.FetchSubscription(context.Background(), target); !errors.Is(err, ErrScheme) {
			t.Fatalf("%s: want ErrScheme, got %v", target, err)
		}
	}

	hops := 0
	redirecting := NewWithOptions(Options{Lookup: lookupAll(publicAddress), Transport: roundTrip(func(request *http.Request) (*http.Response, error) {
		hops++
		next := "https://example.com/hop" + string(rune('0'+hops))
		return &http.Response{StatusCode: 302, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{"Location": []string{next}}}, nil
	})})
	if _, err := redirecting.FetchSubscription(context.Background(), "https://example.com/sub"); !errors.Is(err, ErrRedirects) {
		t.Fatalf("want ErrRedirects, got %v", err)
	}
}

func TestRedirectToPrivateTargetIsRejected(t *testing.T) {
	transport := roundTrip(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{"Location": []string{"https://169.254.169.254/latest"}}}, nil
	})
	fetcher := NewWithOptions(Options{Lookup: lookupAll(publicAddress), Transport: transport})
	if _, err := fetcher.FetchSubscription(context.Background(), "https://example.com/sub"); !errors.Is(err, ErrTarget) {
		t.Fatalf("want ErrTarget, got %v", err)
	}
}

func TestBoundedBodyAndFailureCodes(t *testing.T) {
	oversized := NewWithOptions(Options{Lookup: lookupAll(publicAddress), Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return response(200, strings.Repeat("a", maxBodyBytes+1)), nil
	})})
	if _, err := oversized.FetchSubscription(context.Background(), "https://example.com/sub"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}

	empty := NewWithOptions(Options{Lookup: lookupAll(publicAddress), Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return response(200, ""), nil
	})})
	if _, err := empty.FetchSubscription(context.Background(), "https://example.com/sub"); !errors.Is(err, ErrFailed) {
		t.Fatalf("want ErrFailed for empty body, got %v", err)
	}

	failing := NewWithOptions(Options{Lookup: lookupAll(publicAddress), Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused to https://secret.example/sub?token=abc")
	})})
	if _, err := failing.FetchSubscription(context.Background(), "https://secret.example/sub?token=abc"); !errors.Is(err, ErrFailed) {
		t.Fatalf("want ErrFailed, got %v", err)
	} else if strings.Contains(err.Error(), "token") || strings.Contains(err.Error(), "secret.example") {
		t.Fatalf("error leaked the private URL: %v", err)
	}

	missing := NewWithOptions(Options{Lookup: lookupAll(publicAddress), Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return response(404, "not found"), nil
	})})
	if _, err := missing.FetchSubscription(context.Background(), "https://example.com/sub"); !errors.Is(err, ErrHTTP) {
		t.Fatalf("want ErrHTTP for 404, got %v", err)
	}
}

func TestSuccessfulBoundedFetchAndCancellation(t *testing.T) {
	fetcher := NewWithOptions(Options{Lookup: lookupAll(publicAddress), Transport: roundTrip(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("User-Agent") != UserAgent {
			t.Errorf("unexpected user agent %q", request.Header.Get("User-Agent"))
		}
		return response(200, "vless://node\nvmess://node"), nil
	})})
	body, err := fetcher.FetchSubscription(context.Background(), "https://example.com/sub")
	if err != nil || !strings.Contains(string(body), "vless://") {
		t.Fatalf("body=%q err=%v", body, err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	blocking := NewWithOptions(Options{Lookup: lookupAll(publicAddress), Transport: roundTrip(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})})
	if _, err := blocking.FetchSubscription(cancelled, "https://example.com/sub"); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestFailureClassificationNeverLeaksWrappedURL(t *testing.T) {
	ctx := context.Background()
	err := &url.Error{Op: "Get", URL: "https://private.test/sub?token=secret", Err: ErrTarget}
	if got := classify(ctx, ctx, err); got != ErrTarget {
		t.Fatal("wrapped private URL escaped classification")
	}
}

func TestRequestPinsCheckedDNSWithoutResolvingAgain(t *testing.T) {
	calls := 0
	f := NewWithOptions(Options{Lookup: func(context.Context, string) ([]netip.Addr, error) {
		calls++
		if calls == 1 {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}, DialContext: func(_ context.Context, _, address string) (net.Conn, error) {
		if address != "8.8.8.8:443" {
			t.Error("unchecked destination", address)
		}
		return nil, &net.OpError{Op: "dial", Err: errors.New("fixture")}
	}})
	if _, err := f.FetchSubscription(context.Background(), "https://subscription.example.test/a"); !errors.Is(err, ErrConnection) {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("request repeated DNS", calls)
	}
}

func TestRequestReportKeepsHTTPStatusAndSourcePrivate(t *testing.T) {
	f := NewWithOptions(Options{Lookup: lookupAll(publicAddress), Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("User-Agent") != "Custom/1" {
			t.Error("custom UA not sent")
		}
		return response(403, "private response"), nil
	})})
	_, report, err := f.FetchSubscriptionRequest(context.Background(), "https://example.com/sub?token=PRIVATE", "Custom/1")
	if !errors.Is(err, ErrForbidden) || report.Status != 403 || report.Phase != "response" || report.At.IsZero() || report.Code != "fetch_forbidden" {
		t.Fatal(report, err)
	}
}

func TestInjectedDialReceivesValidatedLiteralInsteadOfHostname(t *testing.T) {
	var destination string
	f := NewWithOptions(Options{Lookup: lookupAll("8.8.8.8"), DialContext: func(_ context.Context, _, address string) (net.Conn, error) {
		destination = address
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("fixture-unreachable")}
	}})
	_, err := f.FetchSubscription(context.Background(), "https://subscription.example.test/a")
	if destination != "8.8.8.8:443" || !errors.Is(err, ErrConnection) {
		t.Fatal("target was not pinned", destination, err)
	}
}
