package controller

import (
	"context"
	"errors"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/telemetry"
)

// CoreSession is the unprivileged product owner's root-process boundary.
// Only the existing proxy queue may apply, clear or shut down the session.
type CoreSession interface {
	Apply(context.Context, config.RouteConfig) error
	Clear(context.Context) error
	Shutdown(context.Context) error
}

// ConfigureCoreSession installs the root adapter before accepting product RPCs.
func (c *Controller) ConfigureCoreSession(session CoreSession) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.coreSession = session
}

func (c *Controller) executeCoreIntent(ctx context.Context, intent proxyIntent, connection *connectionState) (string, []string) {
	if intent.enabled {
		profile := config.RouteConfig{}
		if c.routes != nil {
			profile, _ = c.diagnosticProfile(ctx, "")
		}
		c.beginConnectionAttempt(intent.id, profile)
	}
	c.mu.Lock()
	core := c.coreSession
	c.mu.Unlock()
	if core == nil {
		c.proxy.desired.Store(false)
		if intent.enabled {
			c.connectionStep("core", time.Now(), "core_required")
			return "core_required", nil
		}
		return "", nil
	}
	if !intent.enabled {
		started := time.Now()
		c.recordCoreStep(intent, "core_release", started, "pending", nil)
		err := core.Clear(ctx)
		code, params := catalogFailure(err)
		c.recordCoreStep(intent, "core_release", started, code, params)
		if err != nil {
			connection.cleanupFailed = true
			c.proxyState(pb.ProtectionStatus_PROTECTION_STATUS_DEGRADED, "stop_failed", nil)
			return "stop_failed", nil
		}
		c.sampleUsage(ctx, true)
		connection.running = false
		connection.cleanupFailed = false
		c.proxy.running = runningBinding{}
		c.proxyState(pb.ProtectionStatus_PROTECTION_STATUS_OFF, "", nil)
		if c.routes != nil {
			next := c.routes.catalog.Clone()
			if c.pruneRemoved(ctx, &next) {
				if err := c.saveCatalog(ctx, next); err != nil {
					return catalogErrorCode(err), nil
				}
			}
		}
		return "", nil
	}
	prepared := time.Now()
	c.recordCoreStep(intent, "core_prepare", prepared, "pending", nil)
	profile, err := c.resolveProfile(ctx)
	if err == nil {
		c.lastConnectionAttempt.TargetRevision = profile.ResolvedConfigRevision
	}
	if err == nil && profile.TLSResolved.NeedsConfirmation {
		err = config.ErrTLSConfirmation
	}
	code, params := catalogFailure(err)
	c.recordCoreStep(intent, "core_prepare", prepared, code, params)
	if err != nil {
		if errors.Is(err, config.ErrTLSConfirmation) {
			return "tls_confirmation_required", nil
		}
		return catalogErrorCode(err), nil
	}
	if ctx.Err() != nil || c.proxy.stopSerial.Load() > intent.serial {
		return "operation_cancelled", nil
	}
	c.proxyState(pb.ProtectionStatus_PROTECTION_STATUS_STARTING, "", &profile.NodeProfile)
	applied := time.Now()
	c.recordCoreStep(intent, "core_apply", applied, "pending", nil)
	err = c.applyCore(ctx, profile)
	code, params = catalogFailure(err)
	c.recordCoreStep(intent, "core_apply", applied, code, params)
	if err == nil && (ctx.Err() != nil || c.proxy.stopSerial.Load() > intent.serial) {
		err = errors.New("operation_cancelled")
	}
	if err != nil {
		if err.Error() == "core_required" || err.Error() == "core_identity_mismatch" {
			c.proxy.desired.Store(false)
			state := pb.ProtectionStatus_PROTECTION_STATUS_OFF
			if connection.running {
				state = pb.ProtectionStatus_PROTECTION_STATUS_DEGRADED
			}
			c.proxyState(state, err.Error(), nil)
			return err.Error(), nil
		}
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer done()
		released := time.Now()
		c.recordCoreStep(intent, "core_rollback", released, "pending", nil)
		clearErr := core.Clear(cleanup)
		clearCode, clearParams := catalogFailure(clearErr)
		c.recordCoreStep(intent, "core_rollback", released, clearCode, clearParams)
		if clearErr != nil {
			connection.running = true
			connection.cleanupFailed = true
			c.proxyState(pb.ProtectionStatus_PROTECTION_STATUS_DEGRADED, "stop_failed", nil)
			return "stop_failed", nil
		}
		connection.running = false
		c.proxy.desired.Store(false)
		c.proxy.running = runningBinding{}
		c.proxyState(pb.ProtectionStatus_PROTECTION_STATUS_OFF, "", nil)
		failure, params := catalogFailure(err)
		return failure, params
	}
	connection.running = true
	connection.cleanupFailed = false
	c.activeTLS(profile)
	// STARTED proves lifecycle only. Do not invent exit/DNS/TLS probe evidence.
	c.proxyState(pb.ProtectionStatus_PROTECTION_STATUS_DEGRADED, "protection_unverified", &profile.NodeProfile)
	c.startCurrentPath(profile, intent.id)
	return "", nil
}

func (c *Controller) shutdownRuntime(ctx context.Context) error {
	c.mu.Lock()
	core := c.coreSession
	c.mu.Unlock()
	if core != nil {
		if err := core.Clear(ctx); err != nil {
			return err
		}
		c.sampleUsage(ctx, true)
		c.drainHistory(ctx)
		return core.Shutdown(ctx)
	}
	return nil
}

// These bounded stage events are retained for manual ON/OFF acceptance. They
// describe lifecycle preparation/release, not verified Internet connectivity.
func (c *Controller) recordCoreStep(intent proxyIntent, stage string, started time.Time, code string, params []string) {
	if intent.enabled && code != "pending" {
		kind := ""
		if stage == "core_prepare" {
			kind = "configuration"
		}
		if stage == "core_apply" {
			kind = "core"
		}
		if kind != "" {
			c.connectionStep(kind, started, diagnosticCause(code, params))
		}
		if code == "tun_conflict_other" {
			c.connectionStep("third_party", started, code)
		}
	}
	phase := "off"
	if intent.enabled {
		phase = "on"
	}
	reason := ""
	if len(params) > 0 {
		reason = params[0]
	}
	telemetry.RecordDiagnostic(telemetry.Diagnostic{
		OperationID: intent.id, Stage: stage, Phase: phase, Code: code, Reason: reason,
		Success: code == "", DurationMS: time.Since(started).Milliseconds(),
	})
}
