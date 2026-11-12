package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"

	"pax-connect/daemon/internal/config"
)

// ConfigureAdblockRules installs the reviewed offline baseline once before owners
// start. Existing content, enabled state and rollback choice survive App upgrades.
func (c *Controller) ConfigureAdblockRules(ctx context.Context, directory string) error {
	o := c.routing
	if o == nil || o.loadError != nil {
		return config.ErrRoutingLibrary
	}
	for _, p := range o.library.Packages {
		if p.ID == config.AdblockPackageID {
			return nil
		}
	}
	raw, err := os.ReadFile(filepath.Join(directory, "adblock-lite.srs"))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != "797806a488e601699ebd75930c56db77881ab9de28023a2dc84ed9794cdbf48d" {
		return config.ErrRoutingLibrary
	}
	entries, version, err := o.decode(raw)
	if err != nil {
		return err
	}
	if !validAdblockEntries(entries) {
		return config.ErrRoutingLibrary
	}
	next := o.library.Clone()
	next.Packages = slices.Insert(next.Packages, len(next.Packages)-1, config.RoutingPackage{
		ID: config.AdblockPackageID, Name: "广告拦截", Kind: "adblock", Destination: "block",
		Source: config.AdblockSource, Version: version, Rules: entries, Automatic: true,
	})
	next.Revision++
	if err := o.store.Save(ctx, next); err != nil {
		return err
	}
	o.library = next
	return nil
}

// The built-in ad source is domain-only: never accept process/IP predicates or
// nested logic from a changed upstream artifact under this product identity.
func validAdblockEntries(entries []config.RoutingEntry) bool {
	if len(entries) == 0 {
		return false
	}
	for _, entry := range entries {
		var predicates map[string][]string
		if json.Unmarshal(entry.Match, &predicates) != nil || len(predicates) == 0 {
			return false
		}
		for key, values := range predicates {
			if (key != "domain" && key != "domain_suffix") || len(values) == 0 {
				return false
			}
		}
	}
	return true
}
