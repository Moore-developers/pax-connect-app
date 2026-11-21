package singbox

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/sagernet/sing-box/daemon"
	"github.com/sagernet/sing-box/experimental/paxobs"
)

// fixedRouteProtection is owned by the root lifecycle owner. The gate alone is
// shared with forwarding workers; it never closes a multiplexed proxy session.
type fixedRouteProtection struct {
	r           *CoreRuntime
	gate        *routeGate
	baseProfile string
	baseSources []string
	tags        map[string]bool
	blocked     map[string]bool
	status      map[string]*fixedRouteClosure
	dirty       bool
}

type fixedRouteClosure struct {
	ids             map[string]bool
	closed          uint64
	pending         bool
	reportedClosed  uint64
	reportedPending bool
	reported        bool
}

func newFixedRouteProtection(r *CoreRuntime) *fixedRouteProtection {
	return &fixedRouteProtection{r: r, gate: &routeGate{}, tags: map[string]bool{}, blocked: map[string]bool{}, status: map[string]*fixedRouteClosure{}}
}

// configure freezes the intent table, before any runtime rejection is applied.
// A rule edit preserves a fault only while that same fixed route is referenced.
func (f *fixedRouteProtection) configure(profile string) error {
	tags, err := fixedRoutingTags(profile)
	if err != nil {
		return err
	}
	for tag := range f.blocked {
		if !tags[tag] {
			delete(f.blocked, tag)
		}
	}
	for tag := range f.status {
		if !tags[tag] {
			delete(f.status, tag)
		}
	}
	f.baseProfile, f.baseSources, f.tags = profile, paxobs.RuleNames(f.r.ctx), tags
	f.gate.configure(tags)
	return nil
}

func fixedRoutingTags(profile string) (map[string]bool, error) {
	doc, err := fixedRoutingDocument(profile)
	if err != nil {
		return nil, err
	}
	tags := map[string]bool{}
	for _, rule := range fixedRoutingRules(doc, "route") {
		if tag, _ := rule["outbound"].(string); tag != "" && tag != "proxy" && tag != "direct" {
			tags[tag] = true
		}
	}
	return tags, nil
}

// applyProfile keeps every predicate and its priority, changing only the action
// for the failed route. Selector, healthy fixed routes and direct remain intact.
func (f *fixedRouteProtection) applyProfile(profile string) (string, error) {
	if len(f.blocked) == 0 {
		return profile, nil
	}
	doc, err := fixedRoutingDocument(profile)
	if err != nil {
		return "", err
	}
	for _, rule := range fixedRoutingRules(doc, "route") {
		if tag, _ := rule["outbound"].(string); f.blocked[tag] {
			delete(rule, "outbound")
			rule["action"], rule["no_drop"] = "reject", true
		}
	}
	for _, rule := range fixedRoutingRules(doc, "dns") {
		server, _ := rule["server"].(string)
		for tag := range f.blocked {
			if server == "dns-route-"+tag {
				delete(rule, "server")
				rule["action"], rule["no_drop"] = "reject", true
				break
			}
		}
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return "", ErrConfig
	}
	return string(raw), nil
}

func (f *fixedRouteProtection) applySources(profile string, sources []string) []string {
	result := slices.Clone(sources)
	doc, err := fixedRoutingDocument(profile)
	if err != nil {
		return result
	}
	for i, rule := range fixedRoutingRules(doc, "route") {
		tag, _ := rule["outbound"].(string)
		if !f.blocked[tag] || i >= len(result) {
			continue
		}
		var evidence map[string]any
		if json.Unmarshal([]byte(result[i]), &evidence) != nil || evidence["Package"] == nil {
			continue
		}
		name, _ := evidence["FixedRouteName"].(string)
		if name == "" {
			name = tag
		}
		evidence["UnavailableRoute"] = name
		raw, _ := json.Marshal(evidence)
		result[i] = string(raw)
	}
	return result
}

// protect first prevents admission and cancels pending work, then replaces the
// tables and sweeps native trackers. A close ACK alone never counts as closed.
func (f *fixedRouteProtection) protect(ctx context.Context, tag string, blocked bool) (uint64, bool, error) {
	if !f.tags[tag] {
		return 0, false, errors.New("fixed_route_not_configured")
	}
	if !blocked {
		if !f.blocked[tag] {
			return 0, false, nil
		}
		_, pending, err := f.closeTag(ctx, tag)
		if err != nil || pending {
			return 0, true, errors.Join(errors.New("fixed_route_connections_pending"), err)
		}
		delete(f.blocked, tag)
		if err := f.replace(ctx); err != nil {
			f.blocked[tag] = true
			f.dirty = true
			f.status[tag].pending = true
			return 0, true, err
		}
		// Recovery opens admission only after both restored tables are installed.
		f.gate.setBlocked(tag, false)
		return 0, false, nil
	}
	if f.status == nil {
		f.status = map[string]*fixedRouteClosure{}
	}
	if !f.blocked[tag] {
		f.status[tag] = &fixedRouteClosure{ids: map[string]bool{}}
	}
	state := f.status[tag]
	before, snapshotErr := f.r.activeNativeConnections(ctx)
	for _, connection := range before {
		if fixedConnectionUses(connection, tag) {
			state.ids[connection.Id] = true
		}
	}
	f.blocked[tag] = true
	f.gate.setBlocked(tag, true)
	replaceErr := f.replace(ctx)
	f.dirty = replaceErr != nil
	_, pending, closeErr := f.closeTag(ctx, tag)
	state.pending = pending || snapshotErr != nil || replaceErr != nil
	return state.closed, state.pending, errors.Join(snapshotErr, replaceErr, closeErr)
}

func (f *fixedRouteProtection) replace(ctx context.Context) error {
	profile, err := f.applyProfile(f.baseProfile)
	if err != nil {
		return err
	}
	paxobs.SetRuleNames(f.r.ctx, f.applySources(f.baseProfile, f.baseSources))
	defer paxobs.SetRuleNames(f.r.ctx, f.baseSources)
	return f.r.replaceRouting(ctx, profile)
}

func (f *fixedRouteProtection) closeTag(ctx context.Context, tag string) (uint64, bool, error) {
	state := f.status[tag]
	current, err := f.r.activeNativeConnections(ctx)
	if err != nil {
		state.pending = true
		return state.closed, true, err
	}
	var closeErr error
	for _, connection := range current {
		if !fixedConnectionUses(connection, tag) {
			continue
		}
		state.ids[connection.Id] = true
		if _, err = f.r.current().CloseConnection(ctx, &daemon.CloseConnectionRequest{Id: connection.Id}); err != nil {
			closeErr = errors.Join(closeErr, errors.New("fixed_route_connections_pending"))
		}
	}
	remaining, err := f.r.activeNativeConnections(ctx)
	if err != nil {
		state.pending = true
		return state.closed, true, errors.Join(closeErr, err)
	}
	active := map[string]bool{}
	pending := false
	for _, connection := range remaining {
		active[connection.Id] = true
		pending = pending || fixedConnectionUses(connection, tag)
	}
	for id := range state.ids {
		if !active[id] {
			state.closed++
			delete(state.ids, id)
		}
	}
	state.pending = pending || f.dirty || f.gate.pending(tag) > 0 || closeErr != nil
	return state.closed, state.pending, closeErr
}

func (f *fixedRouteProtection) maintain(ctx context.Context) {
	if f.dirty {
		f.dirty = f.replace(ctx) != nil
	}
	for tag := range f.blocked {
		_, _, _ = f.closeTag(ctx, tag)
	}
}

// activeTags includes blocked paths so the owner keeps checking recovery even
// after their last connection has been closed. Recent attempts are added by it.
func (f *fixedRouteProtection) activeTags(_ context.Context) map[string]bool {
	active := f.gate.candidates(time.Now())
	for tag := range f.blocked {
		active[tag] = true
	}
	return active
}

func (r *CoreRuntime) activeNativeConnections(ctx context.Context) ([]*daemon.Connection, error) {
	if r.Status() != "STARTED" {
		return nil, errors.New("core_not_started")
	}
	snapshot := &connectionSnapshot{ctx: ctx}
	_ = r.current().SubscribeConnections(&daemon.SubscribeConnectionsRequest{}, snapshot)
	if snapshot.events == nil {
		return nil, errors.New("core_connections_unavailable")
	}
	var active []*daemon.Connection
	for _, event := range snapshot.events.Events {
		if connection := event.Connection; connection != nil && connection.ClosedAt == 0 {
			active = append(active, connection)
		}
	}
	return active, nil
}

func fixedConnectionUses(connection *daemon.Connection, tag string) bool {
	return connection != nil && connection.ClosedAt == 0 && connection.Outbound != "proxy" &&
		!slices.Contains(connection.ChainList, "proxy") &&
		(connection.Outbound == tag || slices.Contains(connection.ChainList, tag))
}

func fixedRoutingDocument(profile string) (map[string]any, error) {
	var doc map[string]any
	if json.Unmarshal([]byte(profile), &doc) != nil || doc == nil {
		return nil, ErrConfig
	}
	return doc, nil
}

func fixedRoutingRules(doc map[string]any, section string) []map[string]any {
	owner, _ := doc[section].(map[string]any)
	values, _ := owner["rules"].([]any)
	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if rule, ok := value.(map[string]any); ok {
			result = append(result, rule)
		}
	}
	return result
}
