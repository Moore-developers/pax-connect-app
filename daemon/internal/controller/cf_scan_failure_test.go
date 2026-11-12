package controller

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	cf "pax-connect/daemon/internal/cfoptimizer"
	"pax-connect/daemon/internal/network"
	"pax-connect/daemon/internal/storage"
)

type rejectScanWrite struct{ *memoryCFFile }

func (r *rejectScanWrite) Write(context.Context, []byte) error { return errors.New("write failed") }

func TestCFScanFailureDoesNotReplaceSavedSet(t *testing.T) {
	for _, mode := range []string{"storage", "binding", "network"} {
		t.Run(mode, func(t *testing.T) {
			old := oldScanData(t)
			file := &memoryCFFile{data: old}
			var repo CFRepository = file
			if mode == "storage" {
				repo = &rejectScanWrite{file}
			}
			var changed atomic.Bool
			calls := 0
			probe := successfulScanProbe()
			probe.Validate = func(context.Context) error {
				calls++
				if mode == "binding" {
					return cf.ErrEnvironment
				}
				if mode == "network" && calls == 3 {
					changed.Store(true)
				}
				return nil
			}
			c := newTestController(t, &fakeTester{})
			c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
				epoch := "A"
				if changed.Load() {
					epoch = "B"
				}
				return network.Physical{Name: "en0", Index: 1, Epoch: epoch}, nil
			}, func(context.Context, network.Physical) (string, string, string) { return "", "", "" })
			c.ConfigureCFCandidates()
			c.ConfigureCFWorkspace(repo, func(network.Physical) cf.Probe { return probe })
			sink, stop := operationSink(c)
			defer stop()
			ack, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "scan"})
			if err != nil {
				t.Fatal(err)
			}
			result := awaitResult(t, sink, ack.OperationId)
			if result.Ok {
				t.Fatal("failed scan reported success")
			}
			raw, _, err := file.Read()
			if err != nil || !bytes.Equal(raw, old) {
				t.Fatal("failed scan changed saved evidence", err)
			}
			w, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if len(w.Records) != 0 || w.Phase != "scan_incomplete" {
				t.Fatal("partial/old set presented as scan result", w)
			}
		})
	}
}

func TestCFScanPublishesNewSuccessBeforeCancellationButDoesNotSavePartial(t *testing.T) {
	old := oldScanData(t)
	repo := &memoryCFFile{data: old}
	var precision atomic.Bool
	probe := successfulScanProbe()
	original := probe.TCP
	validations := 0
	probe.Validate = func(context.Context) error {
		validations++
		if validations == 2 {
			precision.Store(true)
		}
		return nil
	}
	probe.TCP = func(ctx context.Context, ip string) (time.Duration, error) {
		if !precision.Load() || ip == "104.16.0.1" {
			return original(ctx, ip)
		}
		<-ctx.Done()
		return 0, ctx.Err()
	}
	c := scanFixture(t, repo, probe)
	sink, stop := operationSink(c)
	defer stop()
	ack, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "scan"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = c.CancelOperation(context.Background(), &pb.CancelOperationRequest{OperationId: ack.OperationId})
	}()
	deadline := time.Now().Add(time.Second)
	for {
		w, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if len(w.Records) > 0 {
			if len(w.Records) != 1 || w.Records[0].Ip != "104.16.0.1" || w.Records[0].Download.Value != 0 || w.OperationId == "" {
				t.Fatal("old data mixed into in-flight scan", w)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completed precise success not published")
		}
		time.Sleep(time.Millisecond)
	}
	_, err = c.CancelOperation(context.Background(), &pb.CancelOperationRequest{OperationId: ack.OperationId})
	if err != nil {
		t.Fatal(err)
	}
	if awaitResult(t, sink, ack.OperationId).Ok {
		t.Fatal("cancelled scan succeeded")
	}
	raw, _, err := repo.Read()
	if err != nil || !bytes.Equal(raw, old) {
		t.Fatal("partial scan overwritten saved set")
	}
	w, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{})
	if err != nil || len(w.Records) != 0 || w.Phase != "scan_incomplete" {
		t.Fatal("cancelled rows still advertised", w, err)
	}
}

type uncertainScanWrite struct{ *memoryCFFile }

func (r *uncertainScanWrite) Write(ctx context.Context, b []byte) error {
	if err := r.memoryCFFile.Write(ctx, b); err != nil {
		return err
	}
	return storage.ErrCommitUncertain
}
func TestCFScanUncertainCommitDoesNotClaimSuccessOrOverwriteDisk(t *testing.T) {
	repo := &uncertainScanWrite{&memoryCFFile{data: oldScanData(t)}}
	c := scanFixture(t, repo, successfulScanProbe())
	sink, stop := operationSink(c)
	defer stop()
	ack, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "scan"})
	if err != nil {
		t.Fatal(err)
	}
	result := awaitResult(t, sink, ack.OperationId)
	if result.Ok || result.Error.GetCode() != "cf_cache_save_failed" {
		t.Fatal("uncertain commit reported saved", result)
	}
	if _, err = c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "scan"}); err == nil {
		t.Fatal("uncertain file still writable")
	}
	c.ConfigureCFWorkspace(repo.memoryCFFile, func(network.Physical) cf.Probe { return successfulScanProbe() })
	w, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{})
	if err != nil || len(w.Records) != 1 || w.Records[0].Tcp.Value != 90 {
		t.Fatal("reopen did not reconcile complete committed file", w, err)
	}
}

type blockingUncertainScanWrite struct {
	*memoryCFFile
	entered chan struct{}
	release chan struct{}
	first   atomic.Bool
}

func (r *blockingUncertainScanWrite) Write(ctx context.Context, b []byte) error {
	if r.first.CompareAndSwap(false, true) {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := r.memoryCFFile.Write(ctx, b); err != nil {
			return err
		}
		return storage.ErrCommitUncertain
	}
	return r.memoryCFFile.Write(ctx, b)
}
func TestCFQueuedSaveCannotOverwriteUncertainScanCommit(t *testing.T) {
	repo := &blockingUncertainScanWrite{memoryCFFile: &memoryCFFile{data: oldScanData(t)}, entered: make(chan struct{}), release: make(chan struct{})}
	c := scanFixture(t, repo, successfulScanProbe())
	sink, stop := operationSink(c)
	defer stop()
	ack, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "scan"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-repo.entered:
	case <-time.After(time.Second):
		t.Fatal("scan not saving")
	}
	// This is the same save boundary used by a concurrently completed validation.
	started := make(chan struct{})
	saved := make(chan error, 1)
	go func() { close(started); saved <- c.saveCF(context.Background()) }()
	<-started
	for i := 0; i < 100; i++ {
		runtime.Gosched()
	}
	close(repo.release)
	if awaitResult(t, sink, ack.OperationId).Ok {
		t.Fatal("uncertain scan claimed saved")
	}
	if err := <-saved; err == nil {
		t.Fatal("queued save bypassed read-only freeze")
	}
	raw, _, err := repo.Read()
	if err != nil {
		t.Fatal(err)
	}
	d, err := cf.Decode(raw)
	if err != nil || len(d.Records) != 1 || d.Records[0].TCP.Value != 90 {
		t.Fatal("queued old document overwrote committed snapshot", err)
	}
}
