package measurement

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

const DownloadBudget = 8 * 1024 * 1024

// ErrDownloadIncomplete indicates a short or otherwise incomplete sample.
var ErrDownloadIncomplete = errors.New("download_incomplete")
var errDownloadFailed = errors.New("download_failed")

func Download(ctx context.Context, client Client, bytes uint64) (DownloadResult, error) {
	if bytes == 0 || bytes > DownloadBudget {
		return DownloadResult{}, errDownloadFailed
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://speed.cloudflare.com/__down?bytes="+strconv.FormatUint(bytes, 10), nil)
	if err != nil {
		return DownloadResult{}, errDownloadFailed
	}
	request.Header.Set("Accept-Encoding", "identity")
	started := time.Now()
	requestContext, stop := context.WithCancel(ctx)
	defer stop()
	request = request.WithContext(requestContext)
	response, err := client.Do(request)
	if err != nil {
		value := DownloadResult{Setup: time.Since(started), End: "headers_failed"}
		var failure *Failure
		if errors.As(err, &failure) {
			return value, failure
		}
		return value, Classify(err, false)
	}
	defer func() { _ = response.Body.Close() }()
	if encoding := response.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return DownloadResult{}, errDownloadFailed
	}
	if response.StatusCode != http.StatusOK {
		return DownloadResult{}, &Failure{Code: "target_http_failed", Stage: "target_https", Reason: strconv.Itoa(response.StatusCode)}
	}
	setup := time.Since(started)
	bodyStarted := time.Now()
	var window atomic.Bool
	windowDuration := 6 * time.Second
	endReason := "sample_window"
	if deadline, ok := ctx.Deadline(); ok {
		available := time.Until(deadline) - 100*time.Millisecond
		if available < windowDuration {
			windowDuration = max(0, available)
			endReason = "budget_window"
		}
	}
	timer := time.AfterFunc(windowDuration, func() { window.Store(true); stop() })
	defer timer.Stop()
	read, err := io.Copy(io.Discard, io.LimitReader(response.Body, int64(bytes)))
	duration := time.Since(bodyStarted)
	value := DownloadResult{Bytes: uint64(read), Duration: duration, Setup: setup, End: "transfer_failed"}
	if duration > 0 {
		value.BytesPerSecond = float64(read) / duration.Seconds()
	}
	if ctx.Err() != nil {
		value.End = "total_timeout"
		if errors.Is(ctx.Err(), context.Canceled) {
			value.End = "cancelled"
		}
		return value, ctx.Err()
	}
	if window.Load() {
		value.End = endReason
		if read >= 256*1024 && duration >= time.Second {
			return value, nil
		}
		return value, ErrDownloadIncomplete
	}
	if err != nil {
		value.End = "body_interrupted"
		failure := Classify(err, false)
		return value, &Failure{Code: "download_body_interrupted", Stage: "download_body", Reason: failure.Code, cause: err}
	}
	if uint64(read) != bytes {
		value.End = "body_short"
		return value, ErrDownloadIncomplete
	}
	value.End = "byte_cap"
	if duration <= 0 {
		return value, errDownloadFailed
	}
	if bytes >= 256*1024 && duration < time.Second {
		value.End = "sample_short"
		return value, ErrDownloadIncomplete
	}
	return value, nil
}
