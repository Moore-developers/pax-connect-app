package measurement

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

type timedBody struct {
	ctx       context.Context
	remaining int
	closed    bool
}

func (b *timedBody) Read(p []byte) (int, error) {
	if b.remaining > 0 {
		n := min(len(p), b.remaining)
		b.remaining -= n
		return n, nil
	}
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}
func (b *timedBody) Close() error { b.closed = true; return nil }
func TestDownloadWindowIsValidWithoutFullByteBudget(t *testing.T) {
	var body *timedBody
	client := fixtureClient(func(req *http.Request) (*http.Response, error) {
		body = &timedBody{ctx: req.Context(), remaining: 512 * 1024}
		return &http.Response{StatusCode: 200, Body: body}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	value, err := Download(ctx, client, DownloadBudget)
	if err != nil || value.End != "sample_window" || value.Bytes != 512*1024 || value.Duration < 5*time.Second || !body.closed {
		t.Fatal(value, err)
	}
}
func TestDownloadCancellationNeverBecomesWindowSuccess(t *testing.T) {
	client := fixtureClient(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: &timedBody{ctx: req.Context(), remaining: 512 * 1024}}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Download(ctx, client, DownloadBudget)
	if err == nil || err == io.EOF {
		t.Fatal(err)
	}
}

type truncatedDownloadBody struct{ remaining int }

func (b *truncatedDownloadBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	n := min(len(p), b.remaining)
	b.remaining -= n
	return n, nil
}
func (b *truncatedDownloadBody) Close() error { return nil }
func TestDownloadBodyClosureIsNotReportedAsBeforeHTTPSResponse(t *testing.T) {
	client := fixtureClient(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: &truncatedDownloadBody{remaining: 8297775}}, nil
	})
	value, err := Download(context.Background(), client, DownloadBudget)
	var failure *Failure
	if !errors.As(err, &failure) || failure.Code != "download_body_interrupted" || failure.Stage != "download_body" || value.Bytes != 8297775 || value.End != "body_interrupted" {
		t.Fatal("body truncation mislabeled as pre-response close", value.Bytes, value.End, err)
	}
}
