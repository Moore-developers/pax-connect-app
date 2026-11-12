package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

// RoutingLibraryRepository is the sole durable rule configuration boundary.
type RoutingLibraryRepository interface {
	Load() (config.RoutingLibrary, bool, error)
	Save(context.Context, config.RoutingLibrary) error
}
type routingLibraryOwner struct {
	geoReadGate           chan struct{}
	geoBaseline           []config.RoutingEntry
	googleBaselineVersion string
	store                 RoutingLibraryRepository
	library               config.RoutingLibrary
	loadError             error
	apps                  []config.RoutingApplication
	decode                func([]byte) ([]config.RoutingEntry, string, error)
	applicationQuery      func(context.Context) []config.RoutingApplication
	autoJobs              chan *pb.RoutingMutationRequest
	autoDone              chan struct{}
	lastAttempt           map[string]time.Time
	updateErrors          map[string]string
}

// ConfigureRoutingLibrary installs local configuration before starting the serial owner.
func (c *Controller) ConfigureRoutingLibrary(store RoutingLibraryRepository, decode func([]byte) ([]config.RoutingEntry, string, error)) {
	l, _, err := store.Load()
	c.routing = &routingLibraryOwner{geoReadGate: make(chan struct{}, 2), store: store, library: l, loadError: err, decode: decode, applicationQuery: c.localRoutingApplications}
}
func (c *Controller) QueryRoutingLibrary(ctx context.Context, _ *emptypb.Empty) (*pb.RoutingLibrarySnapshot, error) {
	if c.routing == nil {
		return nil, status.Error(codes.Unimplemented, "routing_library_unavailable")
	}
	apps := c.routing.applicationQuery(ctx)
	var result *pb.RoutingLibrarySnapshot
	var failure error
	err := c.query(ctx, func(context.Context) {
		if c.routing.loadError != nil {
			failure = c.routing.loadError
			return
		}
		c.routing.apps = apps
		result = c.routingLibrarySnapshot("")
	})
	if err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, routingFailure(failure)
	}
	return result, nil
}
func (c *Controller) routingLibrarySnapshot(message string) *pb.RoutingLibrarySnapshot {
	l := c.routing.library
	out := &pb.RoutingLibrarySnapshot{Revision: l.Revision, Message: message, State: "configured"}
	c.mu.Lock()
	running := c.current.Status == pb.ProtectionStatus_PROTECTION_STATUS_PROTECTED || c.current.Status == pb.ProtectionStatus_PROTECTION_STATUS_DEGRADED
	for _, r := range c.current.Routes {
		out.Routes = append(out.Routes, &pb.NodeProfile{Id: r.Id, Name: r.Name})
	}
	c.mu.Unlock()
	if running {
		out.State = "active"
	}
	if out.Message == "" {
		if running {
			out.Message = "规则变更仅影响新连接，已建立的连接保持。"
		} else {
			out.Message = "本地规则已保存；当前为直连，开启代理后生效。"
		}
	}
	for _, p := range l.Packages {
		v := &pb.RoutingPackage{Id: p.ID, Name: p.Name, Kind: p.Kind, Destination: p.Destination, Enabled: p.Enabled, Source: p.Source, Version: p.Version, PreviousVersion: p.PreviousVersion, Automatic: p.Automatic, UpdatedUnix: p.LastChecked, UpdateMessage: c.routing.updateErrors[p.ID]}
		rules := p.Rules
		if p.Kind == "geo" {
			rules = config.GeoBuiltinRules()
			v.Version = c.geoDisplayVersion(p)
		}
		for _, r := range rules {
			v.Rules = append(v.Rules, &pb.RoutingEntry{Id: r.ID, Method: r.Method, Target: r.Target, Destination: r.Destination, Enabled: r.Enabled})
		}
		out.Packages = append(out.Packages, v)
	}
	for _, a := range c.routing.apps {
		out.Applications = append(out.Applications, &pb.RoutingApplication{Id: a.ID, Name: a.Name, Domains: a.Domains})
	}
	return out
}

// MutateRoutingLibrary performs slow downloads outside the owner and commits with CAS inside it.
func (c *Controller) MutateRoutingLibrary(ctx context.Context, req *pb.RoutingMutationRequest) (*pb.RoutingLibrarySnapshot, error) {
	if c.routing == nil || req.ExpectedRevision == 0 {
		return nil, status.Error(codes.FailedPrecondition, "routing_library_unavailable")
	}
	var downloaded []config.RoutingEntry
	var version string
	var failure error
	if req.Action == "update_geo" {
		return c.updateGeoLibrary(ctx, req)
	}
	if req.Action == "update" || (req.Action == "save_package" && req.GetPackage().GetKind() == "subscription") {
		source := req.GetPackage().GetSource()
		needDownload := true
		if err := c.query(ctx, func(context.Context) {
			if c.routing.loadError != nil {
				failure = c.routing.loadError
				return
			}
			if c.routing.library.Revision != req.ExpectedRevision {
				failure = config.ErrRoutingConflict
				return
			}
			if req.Action == "save_package" {
				for _, p := range c.routing.library.Packages {
					if p.ID == req.GetPackage().GetId() && p.Kind == "subscription" && p.Source == source {
						needDownload = false
					}
				}
			}
			if req.Action == "update" {
				for _, p := range c.routing.library.Packages {
					if p.ID == req.PackageId && p.CanUpdate() {
						source = p.Source
					}
				}
			}
		}); err != nil {
			return nil, err
		}
		if failure != nil {
			return nil, routingFailure(failure)
		}
		if needDownload {
			if !config.RoutingSourceValid(source) || c.routing.decode == nil {
				return nil, status.Error(codes.InvalidArgument, "routing_source_invalid")
			}
			// Reuse the existing bounded HTTPS source boundary and its concurrency slots.
			if c.subs == nil {
				return nil, status.Error(codes.Unavailable, "routing_source_unavailable")
			}
			select {
			case <-c.subs.slots:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			defer func() { c.subs.slots <- struct{}{} }()
			body, err := c.fetchSource(ctx, source)
			if err != nil {
				code, msg := fetchFailure(err)
				return nil, status.Error(code, msg)
			}
			downloaded, version, err = c.routing.decode(body)
			if err == nil && req.PackageId == config.AdblockPackageID && !validAdblockEntries(downloaded) {
				err = config.ErrRoutingLibrary
			}
			if err != nil {
				return nil, status.Error(codes.InvalidArgument, "routing_source_invalid")
			}
		}
	}
	apps := c.routing.applicationQuery(ctx)
	var result *pb.RoutingLibrarySnapshot
	evidence := operationEvidence("规则操作", "", "", "")
	evidence.Type = "routing." + req.Action
	evidence.Name = map[string]string{"save_rule": "保存规则", "delete_rule": "删除规则", "enable_rule": "启停规则", "update": "更新规则包", "rollback": "回退规则包", "move": "调整规则顺序"}[req.Action]
	if evidence.Name == "" {
		evidence.Name = "修改分流规则"
	}
	err := c.routingCommandEvidence(ctx, evidence, func(ctx context.Context) error {
		func() {
			o := c.routing
			if o.loadError != nil {
				failure = o.loadError
				return
			}
			if o.library.Revision != req.ExpectedRevision {
				failure = config.ErrRoutingConflict
				return
			}
			next := o.library.Clone()
			if failure = mutateRouting(&next, req, downloaded, version, apps); failure != nil {
				return
			}
			next.Revision++
			if failure = next.Validate(); failure != nil {
				return
			}
			if failure = c.commitRoutingLibrary(ctx, next, apps); failure != nil {
				return
			}

			delete(o.updateErrors, req.PackageId)
			message := "已保存；当前为直连，开启代理后生效。"
			c.mu.Lock()
			active := c.current.Status != pb.ProtectionStatus_PROTECTION_STATUS_OFF
			c.mu.Unlock()
			if active {
				message = "已保存并应用，仅影响新连接；已有连接保持。"
			}
			evidence.Sections = []config.EventSection{{Kind: "facts", Title: "保存与应用", Fields: []config.EventField{{Label: "结果", Value: message}, {Label: "配置版本", Value: fmt.Sprint(next.Revision)}}}}
			result = c.routingLibrarySnapshot(message)
		}()
		return failure
	})
	if err != nil {
		return nil, routingFailure(err)
	}
	if failure != nil {
		return nil, routingFailure(failure)
	}
	return result, nil
}
func (c *Controller) routingRouteTags(ctx context.Context) map[string]string {
	out := map[string]string{}
	for _, p := range c.coreProfiles(ctx) {
		out["route:"+p.ID] = config.RuntimeTag(p)
	}
	return out
}
func routingFailure(err error) error {
	if status.Code(err) != codes.Unknown {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}
	code := err.Error()
	switch code {
	case "routing_library_invalid", "routing_source_invalid":
		return status.Error(codes.InvalidArgument, code)
	case "routing_revision_conflict":
		return status.Error(codes.Aborted, code)
	case "routing_route_unavailable", "routing_application_unavailable", "routing_update_unavailable", "routing_update_failed", "routing_recovery_required":
		return status.Error(codes.FailedPrecondition, code)
	default:
		return status.Error(codes.Unavailable, "routing_save_failed")
	}
}
func mutateRouting(l *config.RoutingLibrary, q *pb.RoutingMutationRequest, downloaded []config.RoutingEntry, version string, apps []config.RoutingApplication) error {
	index := slices.IndexFunc(l.Packages, func(p config.RoutingPackage) bool { return p.ID == q.PackageId })
	if q.Action == "save_package" {
		v := q.GetPackage()
		if v == nil {
			return config.ErrRoutingLibrary
		}
		index = slices.IndexFunc(l.Packages, func(p config.RoutingPackage) bool { return p.ID == v.Id })
		if v.Id == config.AdblockPackageID || (v.Kind != "local" && v.Kind != "subscription") {
			return config.ErrRoutingLibrary
		}
		p := config.RoutingPackage{ID: v.Id, Name: strings.TrimSpace(v.Name), Kind: v.Kind, Destination: v.Destination, Enabled: true, Automatic: v.Kind == "subscription", Rules: []config.RoutingEntry{}, Source: v.Source}
		if index >= 0 {
			p = l.Packages[index]
			if p.Kind != v.Kind {
				return config.ErrRoutingLibrary
			}
			p.Name = strings.TrimSpace(v.Name)
			p.Destination = v.Destination
			p.Source = v.Source
		}
		if p.Kind == "subscription" && version != "" {
			if p.Version != version {
				p.PreviousRules = p.Rules
				p.PreviousVersion = p.Version
				p.Rules = downloaded
				p.Version = version
			}
			p.LastChecked = time.Now().Unix()
		}
		if index >= 0 {
			l.Packages[index] = p
		} else {
			at := len(l.Packages) - 1
			if l.Packages[at-1].ID == config.AdblockPackageID {
				at--
			}
			l.Packages = slices.Insert(l.Packages, at, p)
		}
		return nil
	}
	if index < 0 {
		return config.ErrRoutingLibrary
	}
	p := &l.Packages[index]
	switch q.Action {
	case "delete_package":
		if p.Kind == "priority" || p.Kind == "geo" || p.Kind == "adblock" {
			return config.ErrRoutingLibrary
		}
		l.Packages = slices.Delete(l.Packages, index, index+1)
	case "enable_package":
		if p.Kind == "priority" {
			return config.ErrRoutingLibrary
		}
		p.Enabled = q.Enabled
	case "destination":
		if p.Kind == "priority" || p.Kind == "geo" || p.Kind == "adblock" {
			return config.ErrRoutingLibrary
		}
		p.Destination = q.Destination
	case "move":
		if (q.Delta != 1 && q.Delta != -1) || index <= 0 || index >= len(l.Packages)-1 {
			return config.ErrRoutingLibrary
		}
		if p.Kind == "adblock" {
			return config.ErrRoutingLibrary
		}
		other := index + int(q.Delta)
		if other <= 0 || other >= len(l.Packages)-1 || l.Packages[other].Kind == "adblock" {
			return config.ErrRoutingLibrary
		}
		l.Packages[index], l.Packages[other] = l.Packages[other], l.Packages[index]
	case "automatic":
		if !p.CanUpdate() {
			return config.ErrRoutingLibrary
		}
		p.Automatic = q.Enabled
	case "rollback":
		if !p.CanUpdate() || p.PreviousVersion == "" {
			return config.ErrRoutingLibrary
		}
		p.Rules = p.PreviousRules
		p.Version = p.PreviousVersion
		p.PreviousRules = nil
		p.PreviousVersion = ""
		p.Automatic = false
	case "update":
		if !p.CanUpdate() {
			return config.ErrRoutingLibrary
		}
		p.LastChecked = time.Now().Unix()
		if p.Version != version {
			p.PreviousVersion = p.Version
			p.PreviousRules = p.Rules
			p.Rules = downloaded
			p.Version = version
		}
	case "replace_rules":
		if p.Kind != "priority" && p.Kind != "local" {
			return config.ErrRoutingLibrary
		}
		entries := make([]config.RoutingEntry, 0, len(q.Rules))
		for _, v := range q.Rules {
			target, err := config.NormalizeRoutingTarget(v.Method, v.Target)
			if err != nil {
				return err
			}
			if v.Method == "application" && !slices.ContainsFunc(apps, func(a config.RoutingApplication) bool { return a.ID == target }) {
				return errors.New("routing_application_unavailable")
			}
			entries = append(entries, config.RoutingEntry{ID: v.Id, Method: v.Method, Target: target, Destination: v.Destination, Enabled: v.Enabled})
		}
		p.Rules = entries
	case "save_rule", "delete_rule", "enable_rule":
		if p.Kind != "priority" && p.Kind != "local" {
			return config.ErrRoutingLibrary
		}
		ri := slices.IndexFunc(p.Rules, func(r config.RoutingEntry) bool { return r.ID == q.RuleId })
		if q.Action == "delete_rule" || q.Action == "enable_rule" {
			if ri < 0 {
				return config.ErrRoutingLibrary
			}
			if q.Action == "delete_rule" {
				p.Rules = slices.Delete(p.Rules, ri, ri+1)
			} else {
				p.Rules[ri].Enabled = q.Enabled
			}
			break
		}
		v := q.GetRule()
		if v == nil {
			return config.ErrRoutingLibrary
		}
		target, err := config.NormalizeRoutingTarget(v.Method, v.Target)
		if err != nil {
			return err
		}
		if v.Method == "application" && !slices.ContainsFunc(apps, func(a config.RoutingApplication) bool { return a.ID == target }) {
			return errors.New("routing_application_unavailable")
		}
		next := config.RoutingEntry{ID: v.Id, Method: v.Method, Target: target, Destination: v.Destination, Enabled: v.Enabled}
		duplicate := slices.IndexFunc(p.Rules, func(r config.RoutingEntry) bool {
			return r.Method == next.Method && r.Target == next.Target && r.ID != next.ID
		})
		if duplicate >= 0 {
			if p.Kind != "priority" {
				return config.ErrRoutingLibrary
			}
			next.ID = p.Rules[duplicate].ID
			next.Enabled = p.Rules[duplicate].Enabled
			p.Rules[duplicate] = next
			break
		}
		ri = slices.IndexFunc(p.Rules, func(r config.RoutingEntry) bool { return r.ID == next.ID })
		if ri >= 0 {
			p.Rules[ri] = next
		} else if p.Kind == "priority" {
			p.Rules = slices.Insert(p.Rules, 0, next)
		} else {
			p.Rules = append(p.Rules, next)
		}
	default:
		return config.ErrRoutingLibrary
	}
	return nil
}

// References include disabled rules: deleting a route cannot silently break a saved choice.
func (c *Controller) routingReferences(id string) bool {
	if c.routing == nil {
		return false
	}
	for _, p := range c.routing.library.Packages {
		if p.Destination == "route:"+id {
			return true
		}
		if p.Kind == "priority" {
			for _, r := range p.Rules {
				if r.Destination == "route:"+id {
					return true
				}
			}
		}
	}
	return false
}

// routingCommand shares the existing cancellable product operation queue. A Stop
// cancels in-flight core I/O; no mutation is smuggled through a read-only query.
func (c *Controller) routingCommandEvidence(ctx context.Context, evidence *config.SystemEvent, apply func(context.Context) error) error {
	done := make(chan error, 1)
	_, err := c.admitRouteOperation(ctx, &routeOperation{label: "规则操作", evidence: evidence, apply: func(operation context.Context) error {
		if operation.Err() != nil {
			done <- operation.Err()
			return operation.Err()
		}
		err := apply(operation)
		done <- err
		return err
	}})
	if err != nil {
		return err
	}
	select {
	case err := <-done:
		if barrier := c.query(ctx, func(context.Context) {}); barrier != nil {
			return barrier
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-c.proxy.lifetime.Done():
		return context.Canceled
	}
}

// commitRoutingLibrary is the one persistence/runtime commit path for local and Geo edits.
func (c *Controller) commitRoutingLibrary(ctx context.Context, next config.RoutingLibrary, apps []config.RoutingApplication) error {
	o := c.routing
	var failure error
	// Check references even while OFF. No proxy/core is needed to persist valid local rules.
	if _, failure = config.CompileRouting(next, apps, c.routingRouteTags(ctx), true); failure != nil {
		return failure
	}
	old := o.library.Clone()
	if failure = o.store.Save(ctx, next); failure != nil {
		// A durability error can follow rename. Never keep a stale in-memory revision.
		loaded, _, loadErr := o.store.Load()
		if loadErr != nil || loaded.Revision != old.Revision {
			o.loadError = errors.New("routing_recovery_required")
			failure = o.loadError
		}
		return failure
	}
	c.mu.Lock()
	active := c.current.Status != pb.ProtectionStatus_PROTECTION_STATUS_OFF
	c.mu.Unlock()
	if active {
		runtime, ok := c.coreSession.(interface {
			UpdateRouting(context.Context, config.RoutingLibrary, []config.RoutingApplication, map[string]string) error
		})
		if !ok {
			failure = errors.New("routing_update_unavailable")
		} else {
			failure = runtime.UpdateRouting(ctx, next, apps, c.routingRouteTags(ctx))
		}
		// Routing can change the path used by a verification request. Withdraw old
		// evidence for both success and an uncertain apply; never leave a stale pass.
		c.cancelCurrentPath("routing_changed")
		c.proxy.runtimeEpoch++
		if failure != nil {
			recovery, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer done()
			restore := o.store.Save(recovery, old)
			var runtimeRestore error
			if ok {
				runtimeRestore = runtime.UpdateRouting(recovery, old, apps, c.routingRouteTags(recovery))
			}
			if restore != nil || runtimeRestore != nil {
				o.loadError = errors.New("routing_recovery_required")
				failure = o.loadError
				c.proxyState(pb.ProtectionStatus_PROTECTION_STATUS_DEGRADED, "routing_recovery_required", nil)
			}
			return failure
		}
	}
	o.library = next
	o.apps = apps
	return nil
}
