package controller

import (
	"context"
	"errors"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/network"
)

var diagnosticKinds = []string{"configuration", "core", "third_party", "node", "takeover", "https"}

func diagnosticCause(code string, params []string) string {
	if code == "core_profile_start_failed" && len(params) > 0 {
		switch params[0] {
		case "address_in_use", "permission_denied", "resource_unavailable", "configuration_rejected", "route_conflict", "kernel_start_failed":
			return code + ":" + params[0]
		}
	}
	return code
}
func diagnosticStep(kind string, started time.Time, code string) *pb.ConnectionDiagnosticStep {
	state := "pass"
	if code != "" {
		state = "fail"
	}
	return &pb.ConnectionDiagnosticStep{Kind: kind, State: state, Code: code, DurationMs: uint64(max(0, time.Since(started).Milliseconds())), At: timestamppb.Now()}
}
func replaceDiagnosticStep(steps []*pb.ConnectionDiagnosticStep, step *pb.ConnectionDiagnosticStep) []*pb.ConnectionDiagnosticStep {
	for i, v := range steps {
		if v.Kind == step.Kind {
			steps[i] = step
			return steps
		}
	}
	return append(steps, step)
}

// Attempt evidence is mutated only by the existing serial product owner.
func (c *Controller) beginConnectionAttempt(id string, p config.RouteConfig) {
	c.lastConnectionAttempt = &pb.TrafficPathDiagnostics{OperationId: id, TargetRouteId: p.ID, TargetRouteName: p.Name, TargetRevision: p.ResolvedConfigRevision, AttemptAt: timestamppb.Now()}
}
func (c *Controller) connectionStep(kind string, started time.Time, code string) {
	if c.lastConnectionAttempt != nil {
		c.lastConnectionAttempt.Steps = replaceDiagnosticStep(c.lastConnectionAttempt.Steps, diagnosticStep(kind, started, code))
	}
}

type coreObserver interface {
	ObserveCore(context.Context) (config.CoreObservation, error)
}
type routeDiagnoser interface {
	DiagnoseRoute(context.Context, config.RouteConfig) config.ConnectionProbe
}

// Diagnostic resolution reads the same composition/version rules without
// invalidating or saving TLS authorization just because a panel was opened.
func (c *Controller) diagnosticProfile(ctx context.Context, id string) (config.RouteConfig, error) {
	if id == "" {
		id = c.routes.catalog.SelectedID
	}
	record, ok := c.routes.catalog.Find(id)
	if !ok {
		return config.RouteConfig{}, errors.New("route_not_selected")
	}
	var policy func(context.Context, config.RouteConfig) (config.TLSPolicyView, error)
	if c.tls != nil {
		policy = func(_ context.Context, p config.RouteConfig) (config.TLSPolicyView, error) {
			if lookup, ok := c.tls.(interface {
				Lookup(config.RouteConfig) (config.TLSPolicyView, error)
			}); ok {
				return lookup.Lookup(p)
			}
			return config.TLSPolicyView{}, errors.New("tls_observation_unavailable")
		}
	}
	resolved, err := c.resolveRecordPolicy(ctx, record, policy)
	return resolved.Config, err
}

type diagnosticCapture struct {
	report   *pb.TrafficPathDiagnostics
	profile  config.RouteConfig
	boundary pathBoundary
	core     CoreSession
}

func (c *Controller) captureConnectionDiagnosis(ctx context.Context) (diagnosticCapture, error) {
	capture := diagnosticCapture{report: &pb.TrafficPathDiagnostics{At: timestamppb.Now()}}
	err := c.query(ctx, func(ctx context.Context) {
		r := capture.report
		c.mu.Lock()
		running := c.current.GetCurrentRoute()
		r.RunningRouteId, r.RunningRouteName = running.GetId(), running.GetName()
		state := c.current.Status
		capture.boundary = pathBoundary{runtimeEpoch: c.proxy.runtimeEpoch, route: running.GetId(), revision: running.GetConfigRevision(), status: state}
		capture.core = c.coreSession
		c.mu.Unlock()
		if c.proxy.networkEpoch != nil {
			var e error
			capture.boundary.epoch, e = c.proxy.networkEpoch()
			capture.boundary.epochKnown = e == nil
		}
		target := ""
		// A failed running switch rolls the catalog back; keep diagnosing the requested
		// target rather than borrowing the old route's success.
		if state != pb.ProtectionStatus_PROTECTION_STATUS_OFF && c.lastConnectionAttempt != nil {
			target = c.lastConnectionAttempt.TargetRouteId
		}
		started := time.Now()
		profile, e := c.diagnosticProfile(ctx, target)
		capture.profile = profile
		r.TargetRouteId, r.TargetRouteName, r.TargetRevision = profile.ID, profile.Name, profile.ResolvedConfigRevision
		for _, kind := range diagnosticKinds {
			r.Steps = append(r.Steps, &pb.ConnectionDiagnosticStep{Kind: kind, State: "not_checked", Code: "no_evidence"})
		}
		code, params := catalogFailure(e)
		if e == nil && profile.TLSResolved.NeedsConfirmation {
			code = "tls_confirmation_required"
		}
		r.Steps[0] = diagnosticStep("configuration", started, diagnosticCause(code, params))
		if a := c.lastConnectionAttempt; a != nil {
			r.OperationId, r.AttemptAt, r.AttemptRouteName = a.OperationId, a.AttemptAt, a.TargetRouteName
			r.AttemptMatchesTarget = a.TargetRouteId == r.TargetRouteId && a.TargetRevision == r.TargetRevision
			for _, step := range a.Steps {
				r.AttemptSteps = append(r.AttemptSteps, proto.Clone(step).(*pb.ConnectionDiagnosticStep))
			}
			if r.AttemptMatchesTarget {
				for _, step := range r.AttemptSteps {
					if step.Kind != "configuration" {
						r.Steps = replaceDiagnosticStep(r.Steps, proto.Clone(step).(*pb.ConnectionDiagnosticStep))
					}
				}
			}
		}
	})
	if err != nil {
		return diagnosticCapture{}, err
	}
	return capture, nil
}

// DiagnoseConnection defaults to retained evidence. Recheck only reads the OS/core
// and makes one isolated HTTPS request; it cannot apply a profile or switch TUN.
func (c *Controller) DiagnoseConnection(ctx context.Context, req *pb.ConnectionDiagnosticRequest) (*pb.TrafficPathDiagnostics, error) {
	p := c.pathDiagnostics
	if p == nil {
		return nil, status.Error(codes.Unimplemented, "path_diagnostics_unavailable")
	}
	capture, err := c.captureConnectionDiagnosis(ctx)
	if err != nil {
		return nil, err
	}
	result := capture.report
	if !req.Recheck {
		if c.physical != nil {
			observed, done := context.WithTimeout(ctx, time.Second)
			physical, err := c.physical.sample(observed)
			done()
			state, mode := physical.IPv6State, "unknown"
			if err != nil || state == "" {
				state = "unknown"
			}
			if err == nil && physical.IPv4Only() {
				mode = "ipv4"
			} else if state == "available" {
				mode = "dual"
			}
			result.Findings = append(result.Findings, &pb.TrafficPathFinding{Kind: "ip_family", Subject: "connection", State: mode}, &pb.TrafficPathFinding{Kind: "ip_family", Subject: "ipv6", State: state})
		}
		return result, nil
	}
	if !p.mu.TryLock() {
		return nil, status.Error(codes.ResourceExhausted, "path_diagnostics_busy")
	}
	defer p.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if c.tests != nil {
		c.tests.mu.Lock()
		if c.tests.closed {
			c.tests.mu.Unlock()
			return nil, status.Error(codes.Unavailable, "daemon stopping")
		}
		c.tests.probes.Add(1)
		c.tests.mu.Unlock()
		defer c.tests.probes.Done()
		stop := context.AfterFunc(c.tests.lifetime, cancel)
		defer stop()
	}
	result.Rechecked = true
	var physical network.Physical
	if c.physical != nil {
		bounded, done := context.WithTimeout(ctx, time.Second)
		var physicalErr error
		physical, physicalErr = c.physical.sample(bounded)
		done()
		if physicalErr != nil {
			physical = network.Physical{}
		}
	}
	var observation config.CoreObservation
	coreStarted := time.Now()
	if observer, ok := capture.core.(coreObserver); ok {
		bounded, done := context.WithTimeout(ctx, 2*time.Second)
		observation, err = observer.ObserveCore(bounded)
		done()
		code, params := catalogFailure(err)
		if err == nil {
			if observation.Status != "STARTED" {
				code = "core_idle"
			} else if observation.SelectedTag != config.RuntimeTag(capture.profile) {
				code = "target_not_running"
			}
		}
		result.Steps[1] = diagnosticStep("core", coreStarted, diagnosticCause(code, params))
	}
	environmentStarted := time.Now()
	report := network.PathReport{Partial: true}
	if p.inspect != nil {
		bounded, done := context.WithTimeout(ctx, 5*time.Second)
		report = p.inspect(bounded, observation.Interface)
		done()
	}
	result.Partial = report.Partial || !capture.boundary.epochKnown
	result.ChangedDuringCapture = report.Changed
	for _, f := range report.Findings {
		result.Findings = append(result.Findings, &pb.TrafficPathFinding{Kind: f.Kind, Subject: f.Subject, State: f.State, Value: f.Value})
	}
	thirdParty, takeover := diagnoseEnvironment(report, observation, capture)
	thirdParty.DurationMs = uint64(time.Since(environmentStarted).Milliseconds())
	thirdParty.At = timestamppb.Now()
	takeover.DurationMs = thirdParty.DurationMs
	takeover.At = thirdParty.At
	result.Steps[2], result.Steps[4] = thirdParty, takeover
	func() {
		if result.Steps[0].State == "pass" {
			if tester, ok := c.tester.(routeDiagnoser); ok {
				if c.tests != nil {
					select {
					case <-c.tests.slots:
						defer func() { c.tests.slots <- struct{}{} }()
					default:
						result.Steps[3].Code = "measurement_busy"
						result.Steps[5].Code = "measurement_busy"
						return
					}
				}
				if c.physical == nil || physical.Name == "" || physical.Epoch == "" {
					result.Steps[3].Code = "physical_network_unavailable"
					result.Steps[5].Code = "physical_network_unavailable"
					return
				}
				profile := bindRouteNetwork(capture.profile, physical)
				profile.MeasurementNetwork = physical.Epoch
				bounded, done := context.WithTimeout(ctx, 6*time.Second)
				probe := tester.DiagnoseRoute(bounded, profile)
				done()
				if probe.NodeDuration > 0 {
					result.Steps[3] = diagnosticStep("node", time.Now(), probe.NodeCode)
					result.Steps[3].DurationMs = uint64(probe.NodeDuration.Milliseconds())
					if probe.NodeCode == "" && probe.HTTPSCode != "" {
						result.Steps[3].State = "not_checked"
						result.Steps[3].Code = "node_dial_only"
					}
				}
				if probe.NodeCode == "" && (probe.HTTPSDuration > 0 || probe.HTTPSCode != "") {
					result.Steps[5] = diagnosticStep("https", time.Now(), probe.HTTPSCode)
					result.Steps[5].DurationMs = uint64(probe.HTTPSDuration.Milliseconds())
				}
			}
		}
	}()
	if observer, ok := capture.core.(coreObserver); ok && observation.Status != "" {
		bounded, done := context.WithTimeout(ctx, time.Second)
		latest, e := observer.ObserveCore(bounded)
		done()
		if e != nil || latest != observation {
			result.ChangedDuringCapture = true
		}
	}
	if c.physical != nil && physical.Epoch != "" {
		bounded, done := context.WithTimeout(ctx, time.Second)
		latest, e := c.physical.sample(bounded)
		done()
		if e != nil || latest.Epoch != physical.Epoch {
			result.ChangedDuringCapture = true
		}
	}
	after, e := c.captureConnectionDiagnosis(ctx)
	if e != nil {
		return nil, e
	}
	result.ChangedDuringCapture = result.ChangedDuringCapture || capture.boundary != after.boundary || result.TargetRouteId != after.report.TargetRouteId || result.TargetRevision != after.report.TargetRevision || result.OperationId != after.report.OperationId
	if result.ChangedDuringCapture {
		for i, s := range result.Steps {
			result.Steps[i] = &pb.ConnectionDiagnosticStep{Kind: s.Kind, State: "not_checked", Code: "capture_changed"}
		}
	}
	result.At = timestamppb.Now()
	return result, nil
}

func diagnoseEnvironment(report network.PathReport, core config.CoreObservation, capture diagnosticCapture) (*pb.ConnectionDiagnosticStep, *pb.ConnectionDiagnosticStep) {
	third := &pb.ConnectionDiagnosticStep{Kind: "third_party", State: "not_checked", Code: "observation_unavailable"}
	takeover := &pb.ConnectionDiagnosticStep{Kind: "takeover", State: "not_checked", Code: "proxy_off"}
	routes, paxRoutes, proxies := 0, 0, 0
	requiredRoutes := 2
	dns := false
	unknownRoute := false
	for _, f := range report.Findings {
		if f.Kind == "ip_family" && f.Subject == "connection" {
			switch f.State {
			case "ipv4":
				requiredRoutes = 1
			case "unknown":
				unknownRoute = true
			}
		}
		if f.Kind == "route" {
			if f.State == "unknown" {
				unknownRoute = true
				continue
			}
			routes++
			if f.State == "pax" {
				paxRoutes++
			}
			if f.State == "other" && strings.HasPrefix(f.Value, "utun") && (core.Status == "IDLE" || core.Interface != "") {
				third.State, third.Code = "fail", "tun_conflict_other"
			}
		}
		if f.Kind == "system" && (f.Subject == "HTTP" || f.Subject == "HTTPS" || f.Subject == "SOCKS") && f.State == "enabled" && f.Value == "127.0.0.1:17890" {
			proxies++
		}
		if f.Kind == "dns" && f.State == "pax" {
			dns = true
		}
	}
	if third.State != "fail" && routes == requiredRoutes && !unknownRoute {
		for _, f := range report.Findings {
			if f.Kind == "route" && f.State == "other" && strings.HasPrefix(f.Value, "utun") && core.Status != "IDLE" && core.Interface == "" {
				return third, takeover
			}
		}
		third.State, third.Code = "pass", "no_third_party_tun_observed"
	}
	if capture.boundary.status != pb.ProtectionStatus_PROTECTION_STATUS_OFF {
		takeover.Code = "takeover_unconfirmed"
		if capture.report.RunningRouteId != capture.report.TargetRouteId {
			takeover.Code = "target_not_running"
			return third, takeover
		}
		if core.Status == "STARTED" && core.SelectedTag == config.RuntimeTag(capture.profile) && core.Interface != "" && paxRoutes == requiredRoutes && !unknownRoute && proxies == 3 && dns {
			takeover.State, takeover.Code = "pass", "takeover_observed"
		} else if third.State == "fail" {
			takeover.State, takeover.Code = "fail", "tun_conflict_other"
		} else if core.Interface == "" {
			takeover.Code = "tun_unconfirmed"
		} else if core.SelectedTag != config.RuntimeTag(capture.profile) {
			takeover.Code = "runtime_selection_mismatch"
		} else if paxRoutes != requiredRoutes {
			takeover.Code = "route_takeover_unconfirmed"
		} else if proxies != 3 {
			takeover.Code = "system_proxy_not_owned"
		} else if !dns {
			takeover.Code = "dns_takeover_unconfirmed"
		}

	}
	return third, takeover
}
