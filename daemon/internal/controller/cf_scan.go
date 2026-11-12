package controller

import (
	"context"
	"errors"
	"math/rand"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	cf "pax-connect/daemon/internal/cfoptimizer"
	"pax-connect/daemon/internal/network"
	"pax-connect/daemon/internal/storage"
)

// cfScan is temporary state owned by the existing CF operation. Saved evidence
// stays untouched until the complete replacement has been written successfully.
type cfScan struct {
	network string
	records []cf.Record
}

func (c *Controller) runCFScan(ctx context.Context, id string, physical network.Physical, probe cf.Probe, progress func(cf.Progress)) error {
	err := cf.Scan(ctx, physical.Epoch, probe, rand.New(rand.NewSource(time.Now().UnixNano())), func(r cf.Record) {
		c.cf.mu.Lock()
		defer c.cf.mu.Unlock()
		w := c.cf.workspace
		if w.operation == id && w.scan != nil && ctx.Err() == nil {
			w.scan.records = append(w.scan.records, r)
		}
	}, progress)
	if err != nil {
		return err
	}
	if probe.Validate != nil {
		if err = probe.Validate(ctx); err != nil {
			return cf.ErrEnvironment
		}
	}
	latest, err := c.physical.sample(ctx)
	if err != nil || latest.Epoch != physical.Epoch {
		return cf.ErrEnvironment
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return c.commitCFScan(ctx, id, physical.Epoch)
}

func (c *Controller) commitCFScan(ctx context.Context, id, network string) error {
	c.cf.mu.Lock()
	w := c.cf.workspace
	c.cf.mu.Unlock()
	w.saveMu.Lock()
	defer w.saveMu.Unlock()
	c.cf.mu.Lock()
	if w.operation != id || w.scan == nil || w.scan.network != network {
		c.cf.mu.Unlock()
		return context.Canceled
	}
	next := cf.ScanDocument(network, w.scan.records, time.Now())
	w.phase = "scan_saving"
	c.cf.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := cf.Encode(next)
	if err == nil {
		latest, checkErr := c.physical.sample(ctx)
		if checkErr != nil || latest.Epoch != network {
			return cf.ErrEnvironment
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if err == nil && w.repository != nil {
		err = w.repository.Write(ctx, raw)
	}
	if err != nil {
		if errors.Is(err, storage.ErrCommitUncertain) {
			c.cf.mu.Lock()
			w.readOnly = true
			c.cf.mu.Unlock()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errCFCacheSaveFailed
	}
	c.cf.mu.Lock()
	w.document = next
	w.scan = nil
	w.proofs = map[string]time.Time{}
	c.cf.cache = map[string]*pb.CFCandidate{}
	c.cf.mu.Unlock()
	return nil
}
