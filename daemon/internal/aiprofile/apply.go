package aiprofile

import (
	"context"
	"time"
)

// Plan freezes provider-only edits against the currently observed CLI files.
func (m *Manager) Plan(ctx context.Context, r PlanRequest) (Plan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.CLI == DSH && (m.usageOnlyDSH || !m.supportsDSH()) {
		return Plan{}, Unsupported
	}
	if ctx.Err() != nil {
		return Plan{}, ctx.Err()
	}
	if m.blocked {
		return Plan{}, RecoveryRequired
	}
	files, e := m.checkedObservation(r.CLI, r.InspectionID)
	if e != nil {
		return Plan{}, e
	}
	p := planned{Plan: Plan{ID: newID(), CLI: r.CLI, ProfileID: r.ProfileID, ExpiresAt: time.Now().Add(time.Minute)}, request: r, before: files}
	if r.Restore {
		return Plan{}, Unsupported
	}
	profile, ok := m.profiles[r.ProfileID]
	if !ok || profile.Revision != r.Revision {
		return Plan{}, Conflict
	}
	if profile.Kind == "native" {
		p.files, e = m.loadGenerated(profile, r.CLI, template{})
		if e == nil && !sameFiles(files, p.files) {
			return Plan{}, Unsupported
		}
	} else if r.Initialize {
		p.files, e = m.initializeAPI(r.CLI, files, profile.BaseURL, profile.Key, r.ModelID)
		p.Initializes = true
		p.TargetPath = m.targetDir(r.CLI)
	} else {
		p.files, e = patchCurrent(r.CLI, files, profile.BaseURL, profile.Key)
	}
	if e != nil {
		return Plan{}, e
	}
	p.BaseURL = profile.BaseURL
	p.connectionRevision = profile.ConnectionRevision
	if profile.Kind == "native" {
		p.request.TemplateRevision = 0
	}
	if !m.validNativeSet(r.CLI, p.files) {
		return Plan{}, StoreInvalid
	}
	for id, v := range m.plans {
		if time.Now().After(v.ExpiresAt) || v.CLI == r.CLI {
			delete(m.plans, id)
		}
	}
	if len(m.plans) >= 16 {
		return Plan{}, Expired
	}
	m.plans[p.ID] = p
	return p.Plan, nil
}

// Apply commits a frozen plan and retains durable evidence for interruption recovery.
func (m *Manager) Apply(ctx context.Context, r ApplyRequest) (Workspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.OperationID == "" || len(r.OperationID) > 128 {
		return Workspace{}, Invalid
	}
	p, ok := m.plans[r.PlanID]
	if !ok {
		for _, cli := range clients {
			var a application
			if exists, e := m.readState("applied", string(cli), &a); e != nil {
				return Workspace{}, e
			} else if exists && a.OperationID == r.OperationID && a.PlanID == r.PlanID {
				if e := m.repairEvents(ctx); e != nil {
					return Workspace{}, e
				}
				return m.workspace(ctx)
			}
		}
		return Workspace{}, Expired
	}
	if m.usageOnlyDSH && p.CLI == DSH {
		return Workspace{}, Unsupported
	}
	var previous application
	exists, e := m.readState("applied", string(p.CLI), &previous)
	if e != nil {
		return Workspace{}, e
	}
	if exists && previous.OperationID == r.OperationID && previous.PlanID == r.PlanID {
		if e := m.repairEvents(ctx); e != nil {
			return Workspace{}, e
		}
		return m.workspace(ctx)
	}
	if time.Now().After(p.ExpiresAt) {
		return Workspace{}, Expired
	}
	if m.blocked {
		return Workspace{}, RecoveryRequired
	}
	current, e := m.native(p.CLI)
	if e != nil {
		return Workspace{}, e
	}
	if !sameObservation(current, p.before) {
		return Workspace{}, Changed
	}
	if !p.request.Restore {
		profile, ok := m.profiles[p.ProfileID]
		if !ok || profile.Revision != p.request.Revision {
			return Workspace{}, Conflict
		}
		var expected []nativeFile
		if profile.Kind == "native" {
			expected, e = m.loadGenerated(profile, p.CLI, template{})
		} else if p.request.Initialize {
			expected, e = m.initializeAPI(p.CLI, current, profile.BaseURL, profile.Key, p.request.ModelID)
		} else {
			expected, e = patchCurrent(p.CLI, current, profile.BaseURL, profile.Key)
		}
		if e != nil {
			return Workspace{}, e
		}
		if !sameFiles(expected, p.files) {
			return Workspace{}, Conflict
		}

	}

	a := application{ConnectionDigest: connectionDigest(p.CLI, p.files), EventAt: time.Now().UnixNano(), EventName: m.profiles[p.ProfileID].Name, EventMultiplier: m.profiles[p.ProfileID].Multiplier, EventModels: m.profiles[p.ProfileID].ModelMultipliers, Schema: 2, PlanID: r.PlanID, ProfileID: p.ProfileID, ConnectionRevision: p.connectionRevision, TemplateRevision: p.request.TemplateRevision, OperationID: r.OperationID, Files: p.files}
	if m.profiles[p.ProfileID].Kind != "native" {
		a.Files = nil
	}
	if sameFiles(current, p.files) {
		if e = m.writeState(ctx, "applied", string(p.CLI), a); e != nil {
			return Workspace{}, e
		}
		if e := m.repairEvents(ctx); e != nil {
			return Workspace{}, e
		}
		return m.workspace(ctx)
	}
	tx := pending{Schema: 2, CLI: p.CLI, Directory: m.targetDir(p.CLI), Before: current, After: p.files, Application: a}
	if e = m.saveJournal(ctx, tx); e != nil {
		return Workspace{}, e
	}
	if p.Initializes {
		if e = m.prepareTargetDirectory(p.CLI); e != nil {
			if err := m.recover(); err != nil {
				m.blocked = true
				return Workspace{}, RecoveryRequired
			}
			return Workspace{}, e
		}
	}
	for i, f := range p.files {
		if sameFiles([]nativeFile{current[i]}, []nativeFile{f}) {
			continue
		}
		observed, err := m.native(p.CLI)
		expected := append([]nativeFile(nil), current...)
		copy(expected[:i], p.files[:i])
		if err != nil || !sameFiles(observed, expected) {
			e = Changed
			break
		}
		identityChanged := false
		for j := i; j < len(current); j++ {
			if observed[j].Identity != current[j].Identity {
				identityChanged = true
			}
		}
		if identityChanged {
			e = Changed
			break
		}
		if m.renameHook != nil {
			if e = m.renameHook(i); e != nil {
				break
			}
		}
		if e = m.replaceNative(p.CLI, f, current[i].Exists); e != nil {
			break
		}
	}
	if e != nil {
		if err := m.recover(); err != nil {
			m.blocked = true
			return Workspace{}, RecoveryRequired
		}
		var recovered application
		if ok, err := m.readState("applied", string(p.CLI), &recovered); err == nil && ok && recovered.OperationID == r.OperationID && recovered.PlanID == r.PlanID {
			if e := m.repairEvents(context.WithoutCancel(ctx)); e != nil {
				return Workspace{}, e
			}
			return m.workspace(context.WithoutCancel(ctx))
		}
		return Workspace{}, cause(e)
	}
	verified, err := m.native(p.CLI)
	if err != nil || !sameFiles(verified, p.files) {
		m.blocked = true
		return Workspace{}, RecoveryRequired
	}
	if e = m.writeState(context.WithoutCancel(ctx), "applied", string(p.CLI), a); e != nil {
		m.blocked = true
		return Workspace{}, Uncertain
	}
	if e = m.clearJournal(); e != nil {
		m.blocked = true
		return Workspace{}, Uncertain
	}
	if e := m.repairEvents(context.WithoutCancel(ctx)); e != nil {
		return Workspace{}, e
	}
	return m.workspace(context.WithoutCancel(ctx))
}
