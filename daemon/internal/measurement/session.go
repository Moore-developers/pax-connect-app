package measurement

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"slices"
	"time"
)

const SessionLatencyMethod = "https204-median-v3"

// LatencySample separates first response from three reused-session responses.
// Stable is absent unless all three warm requests were valid and reused a connection.
type LatencySample struct {
	Cold, Stable                    time.Duration
	StableSamples                   uint32
	StableError, FirstError, Target string
}

// SessionLatency keeps one target and client for cold plus three warm requests.
// Target fallback only happens before a valid cold response; samples from different
// targets are never pooled. A warm deadline preserves cold, explicit cancel doesn't.
func SessionLatency(ctx context.Context, client Client, targets []string) (LatencySample, error) {
	var last error
	var result LatencySample
	for _, target := range targets {
		result = LatencySample{Target: target}
		duration, _, err := sessionRequest(ctx, client, target)
		if ctx.Err() == context.Canceled {
			return result, ctx.Err()
		}
		if err != nil {
			last = Classify(err, false)
			result.FirstError = Classify(err, false).Code
			var failure *Failure
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			if errors.As(err, &failure) && failure.Code == "tls_certificate_invalid" {
				return result, err
			}
			continue
		}
		result.Cold = duration
		values := make([]time.Duration, 0, 3)
		for attempt := 0; attempt < 3; attempt++ {
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return interruptedSession(ctx, result)
			case <-timer.C:
			}
			duration, reused, err := sessionRequest(ctx, client, target)
			if ctx.Err() == context.Canceled {
				return result, ctx.Err()
			}
			if err == nil && reused {
				values = append(values, duration)
				result.StableSamples++
			}
			if len(values) == 3 {
				slices.Sort(values)
				result.Stable = values[1]
				return result, nil
			}
			if ctx.Err() != nil {
				return interruptedSession(ctx, result)
			}
		}
		result.StableError = "latency_samples_incomplete"
		return result, nil
	}
	if last == nil {
		last = &Failure{Code: "test_target_invalid", Stage: "target_https"}
	}
	return result, last
}
func interruptedSession(ctx context.Context, sample LatencySample) (LatencySample, error) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) && sample.Cold > 0 {
		sample.StableError = "latency_samples_incomplete"
		return sample, nil
	}
	return sample, ctx.Err()
}
func sessionRequest(ctx context.Context, client Client, target string) (time.Duration, bool, error) {
	reused := false
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, target, nil)
	if err != nil {
		return 0, false, &Failure{Code: "test_target_invalid", Stage: "target_https"}
	}
	start := time.Now()
	response, err := client.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		return 0, reused, err
	}
	defer func() { _ = response.Body.Close() }()
	_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 16385))
	if response.StatusCode != http.StatusNoContent {
		return 0, reused, &Failure{Code: "target_http_failed", Stage: "target_https"}
	}
	if readErr != nil {
		return 0, reused, readErr
	}
	return elapsed, reused, nil
}
