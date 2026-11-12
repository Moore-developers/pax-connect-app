package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

// ConfigureGeoRules reads the bundled baseline once. Runtime never reads arbitrary App paths.
func (c *Controller) ConfigureGeoRules(directory string) error {
	if c.routing == nil {
		return errors.New("routing_library_unavailable")
	}
	for _, tag := range []string{"geosite-google", "geosite-cn", "geoip-cn"} {
		raw, err := os.ReadFile(filepath.Join(directory, tag+".srs"))
		if err != nil {
			return err
		}
		entries, version, err := c.routing.decode(raw)
		if err != nil {
			return err
		}
		if tag == "geosite-google" {
			c.routing.googleBaselineVersion = version
		}
		if !validGeoEntries(tag, entries) {
			return config.ErrRoutingLibrary
		}
		for _, e := range entries {
			e.ID = tag + ":" + e.ID
			e.Target = tag
			c.routing.geoBaseline = append(c.routing.geoBaseline, e)
		}
	}
	return nil
}

// updateGeoLibrary fetches only the three reviewed community sources and commits them together.
func (c *Controller) updateGeoLibrary(ctx context.Context, q *pb.RoutingMutationRequest) (*pb.RoutingLibrarySnapshot, error) {
	if c.subs == nil || c.routing == nil {
		return nil, status.Error(codes.Unavailable, "routing_library_unavailable")
	}
	var failure error
	if err := c.query(ctx, func(context.Context) {
		if c.routing.loadError != nil {
			failure = c.routing.loadError
		} else if c.routing.library.Revision != q.ExpectedRevision {
			failure = config.ErrRoutingConflict
		}
	}); err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, routingFailure(failure)
	}
	select {
	case <-c.subs.slots:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { c.subs.slots <- struct{}{} }()
	content := []config.RoutingEntry{}
	versions := []string{}
	for _, tag := range []string{"geosite-google", "geosite-cn", "geoip-cn"} {
		repository := "sing-geosite"
		if tag == "geoip-cn" {
			repository = "sing-geoip"
		}
		address := "https://raw.githubusercontent.com/SagerNet/" + repository + "/rule-set/" + tag + ".srs"
		raw, err := c.fetchSource(ctx, address)
		if err != nil {
			code, msg := fetchFailure(err)
			return nil, status.Error(code, msg)
		}
		entries, version, err := c.routing.decode(raw)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "routing_source_invalid")
		}
		if !validGeoEntries(tag, entries) {
			return nil, status.Error(codes.InvalidArgument, "routing_source_invalid")
		}
		for _, e := range entries {
			e.ID = tag + ":" + e.ID
			e.Target = tag
			content = append(content, e)
		}
		versions = append(versions, version)
	}
	var result *pb.RoutingLibrarySnapshot
	evidence := operationEvidence("规则操作", "", "", "")
	evidence.Type = "routing.geo_update"
	evidence.Name = "更新Geo基础包"
	err := c.routingCommandEvidence(ctx, evidence, func(ctx context.Context) error {
		o := c.routing
		if o.loadError != nil {
			return o.loadError
		}
		if o.library.Revision != q.ExpectedRevision {
			return config.ErrRoutingConflict
		}
		next := o.library.Clone()
		for i := range next.Packages {
			if next.Packages[i].Kind == "geo" {
				next.Packages[i].GeoContent = content
				next.Packages[i].Version = strings.Join(versions, " / ")
				next.Packages[i].LastChecked = time.Now().Unix()
			}
		}
		next.Revision++
		if err := c.commitRoutingLibrary(ctx, next, o.apps); err != nil {
			return err
		}
		evidence.Sections = []config.EventSection{{Kind: "facts", Title: "更新结果", Fields: []config.EventField{{Label: "版本", Value: strings.Join(versions, " / ")}, {Label: "生效范围", Value: "仅影响新连接，旧连接保持"}}}}
		result = c.routingLibrarySnapshot("Geo 基础包已更新；旧连接保持，新连接使用更新后的规则。")
		return nil
	})
	if err != nil {
		return nil, routingFailure(err)
	}
	return result, nil
}

// QueryGeoRules pages the actual predicates, including a never-updated bundled baseline.
func (c *Controller) QueryGeoRules(ctx context.Context, q *pb.GeoRulesQuery) (*pb.GeoRulesPage, error) {
	if c.routing == nil || len(q.Search) > 200 {
		return nil, status.Error(codes.InvalidArgument, "invalid_filter")
	}
	select {
	case c.routing.geoReadGate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-c.routing.geoReadGate }()
	var pack config.RoutingPackage
	var content []config.RoutingEntry
	var failure error
	err := c.query(ctx, func(context.Context) {
		if c.routing.loadError != nil {
			failure = c.routing.loadError
			return
		}
		for _, p := range c.routing.library.Packages {
			if p.Kind == "geo" {
				pack = p
				break
			}
		}
		// Product revisions are immutable; these bytes remain valid if an update commits later.
		content = c.effectiveGeoContent(pack)
		pack.Version = c.geoDisplayVersion(pack)
	})
	if err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, routingFailure(failure)
	}
	entries := []config.RoutingEntry{}
	search := strings.ToLower(strings.TrimSpace(q.Search))
	for _, entry := range content {
		var predicates map[string]json.RawMessage
		if json.Unmarshal(entry.Match, &predicates) != nil {
			return nil, routingFailure(config.ErrRoutingLibrary)
		}
		for _, key := range []string{"domain", "domain_suffix", "domain_regex", "ip_cidr"} {
			var targets []string
			if raw := predicates[key]; raw != nil {
				if json.Unmarshal(raw, &targets) != nil {
					return nil, routingFailure(config.ErrRoutingLibrary)
				}
			}
			for _, target := range targets {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if search != "" && !strings.Contains(strings.ToLower(target+" "+entry.Target), search) {
					continue
				}
				entries = append(entries, config.RoutingEntry{ID: fmt.Sprintf("%s:%s:%s", entry.ID, key, target), Method: key, Target: target, Destination: entry.Target, Enabled: true})
				if len(entries) > 50000 {
					return nil, routingFailure(config.ErrRoutingLibrary)
				}
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Destination+entries[i].Target < entries[j].Destination+entries[j].Target
	})
	start := min(int(q.Offset), len(entries))
	end := min(start+max(1, min(200, int(q.Limit))), len(entries))
	out := &pb.GeoRulesPage{Total: uint32(len(entries)), Version: pack.Version, UpdatedUnix: pack.LastChecked}
	for _, e := range entries[start:end] {
		out.Rules = append(out.Rules, &pb.RoutingEntry{Id: e.ID, Method: e.Method, Target: e.Target, Destination: e.Destination, Enabled: true})
	}
	if end < len(entries) {
		out.NextOffset = uint32(end)
	}
	return out, nil
}

func validGeoEntries(tag string, entries []config.RoutingEntry) bool {
	if len(entries) == 0 || len(entries) > 100 {
		return false
	}
	total := 0
	for _, entry := range entries {
		var m map[string][]string
		if json.Unmarshal(entry.Match, &m) != nil || len(m) == 0 {
			return false
		}
		for key, values := range m {
			if tag == "geoip-cn" && key != "ip_cidr" || (tag == "geosite-cn" || tag == "geosite-google") && key != "domain" && key != "domain_suffix" && key != "domain_regex" {
				return false
			}
			total += len(values)
		}
	}
	return total > 0 && total <= 50000
}

// Older two-source caches retain their CN/IP version; Google alone falls back to
// the pinned bundle until the next explicit three-source update succeeds.
func (c *Controller) effectiveGeoContent(pack config.RoutingPackage) []config.RoutingEntry {
	if len(pack.GeoContent) == 0 {
		return c.routing.geoBaseline
	}
	content := append([]config.RoutingEntry(nil), pack.GeoContent...)
	for _, e := range content {
		if e.Target == "geosite-google" {
			return content
		}
	}
	for _, e := range c.routing.geoBaseline {
		if e.Target == "geosite-google" {
			content = append(content, e)
		}
	}
	return content
}
func (c *Controller) geoDisplayVersion(pack config.RoutingPackage) string {
	for _, e := range pack.GeoContent {
		if e.Target == "geosite-google" {
			return pack.Version
		}
	}
	if c.routing.googleBaselineVersion == "" {
		return pack.Version
	}
	return pack.Version + " / Google 内置 " + c.routing.googleBaselineVersion
}
