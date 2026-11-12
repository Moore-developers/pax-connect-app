package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"pax-connect/daemon/internal/cfoptimizer"
	"pax-connect/daemon/internal/measurement"
	"pax-connect/daemon/internal/network"
	"sync"
	"testing"
	"time"
)

type cfFixtureClient func(*http.Request) (*http.Response, error)

func (f cfFixtureClient) Do(r *http.Request) (*http.Response, error) { return f(r) }

type cfSampleBody struct {
	remaining int
	once      sync.Once
	closed    bool
}

func (b *cfSampleBody) Read(buffer []byte) (int, error) {
	b.once.Do(func() { time.Sleep(1100 * time.Millisecond) })
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(buffer), b.remaining)
	b.remaining -= n
	return n, nil
}
func (b *cfSampleBody) Close() error { b.closed = true; return nil }
func TestCFDownloadUsesSharedSingleHTTPBudget(t *testing.T) {
	calls := 0
	body := &cfSampleBody{remaining: measurement.DownloadBudget}
	client := cfFixtureClient(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.String() != "https://speed.cloudflare.com/__down?bytes=8388608" {
			t.Error("changed shared target or budget")
		}
		deadline, ok := req.Context().Deadline()
		if !ok || time.Until(deadline) > measurement.DownloadTimeout || time.Until(deadline) < 29*time.Second {
			t.Error("CF is not using the shared 30-second deadline")
		}
		if req.Header.Get("Connection") != "" {
			t.Error("forced connection close")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body}, nil
	})
	speed, err := cfDownloadSample(context.Background(), client)
	if err != nil || speed <= 0 || calls != 1 || !body.closed {
		t.Fatal(speed, err, calls, body.closed)
	}
}
func TestCFDownloadSharedSamplerKeepsPhysicalFailureAndCancellation(t *testing.T) {
	_, err := cfDownloadSample(context.Background(), cfFixtureClient(func(*http.Request) (*http.Response, error) { return nil, network.ErrPhysicalBinding }))
	if !errors.Is(err, cfoptimizer.ErrEnvironment) {
		t.Fatal("physical failure blamed remote CF IP", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = cfDownloadSample(ctx, cfFixtureClient(func(req *http.Request) (*http.Response, error) { return nil, req.Context().Err() }))
	if err == nil {
		t.Fatal("cancelled download became successful")
	}
}
