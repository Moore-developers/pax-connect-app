package controller

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/cadence"
	"pax-connect/daemon/internal/config"
)

type trafficAlertOwner struct {
	mu       sync.Mutex
	store    *config.TrafficAlertStore
	settings config.TrafficAlertSettings
	runtime  config.TrafficAlertRuntime
	failure  error
	wake     chan struct{}
	cancel   context.CancelFunc
	done     chan struct{}
	cache    alertWindowCache // loop-owned, never accessed by settings RPCs
}

// ConfigureTrafficAlerts starts the single alert owner after history and operations.
// All task state is private; GetTrafficAlerts reads its published immutable value.
func (c *Controller) ConfigureTrafficAlerts(ctx context.Context, directory string, owner uint32) {
	p := &trafficAlertOwner{store: config.NewTrafficAlertStore(directory, owner), wake: make(chan struct{}, 1), done: make(chan struct{})}
	p.settings, p.failure = p.store.LoadSettings()
	if p.failure == nil {
		p.runtime, p.failure = p.store.LoadRuntime()
	}
	if p.runtime.Revision != p.settings.Revision {
		p.runtime = config.TrafficAlertRuntime{Revision: p.settings.Revision}
	}
	if p.runtime.Episodes == nil {
		p.runtime.Episodes = map[string]config.TrafficAlertEpisode{}
	}
	c.trafficAlerts = p
	c.publishTrafficAlerts(&pb.TrafficAlertState{Settings: alertSettingsProto(p.settings), MonitorState: "checking", ActiveNoticeIds: alertActiveIDs(p.runtime)})
	lifetime, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	go func() {
		defer close(p.done)
		cadence.Loop{Name: "traffic-alerts", RunFirst: true, Interval: func() time.Duration { return cadence.TrafficAlerts }, Wake: []<-chan struct{}{p.wake}, Run: func(ctx context.Context) (bool, error) { c.checkTrafficAlerts(ctx); return false, nil }}.Serve(lifetime)
	}()
}

// CloseTrafficAlerts stops the job before the history read pools are closed.
func (c *Controller) CloseTrafficAlerts(ctx context.Context) error {
	p := c.trafficAlerts
	if p == nil {
		return nil
	}
	p.cancel()
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Controller) publishTrafficAlerts(v *pb.TrafficAlertState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v.Revision = c.current.TrafficAlerts.GetRevision()
	if proto.Equal(c.current.TrafficAlerts, v) {
		return
	}
	v.Revision++
	c.current.TrafficAlerts = proto.Clone(v).(*pb.TrafficAlertState)
	c.snapshotSeq++
	c.current.SnapshotVersion = c.snapshotSeq
	c.fanoutLocked(&pb.WatchEvent{Seq: c.snapshotSeq, SnapshotVersion: c.snapshotSeq, Payload: &pb.WatchEvent_TrafficAlerts{TrafficAlerts: v}})
}

func (c *Controller) checkTrafficAlerts(ctx context.Context) {
	p := c.trafficAlerts
	p.mu.Lock()
	if p.failure != nil {
		settings, err := p.store.LoadSettings()
		if err == nil {
			runtime, loadErr := p.store.LoadRuntime()
			if loadErr == nil {
				if runtime.Revision != settings.Revision {
					runtime = config.TrafficAlertRuntime{Revision: settings.Revision}
				}
				if runtime.Episodes == nil {
					runtime.Episodes = map[string]config.TrafficAlertEpisode{}
				}
				p.settings, p.runtime, p.failure = settings, runtime, nil
			}
		}
	}
	settings, failure := p.settings, p.failure
	p.mu.Unlock()
	state := &pb.TrafficAlertState{Settings: alertSettingsProto(settings)}
	switch {
	case failure != nil:
		state.MonitorState = "failed"
		state.ErrorCode = "traffic_alerts_storage_failed"
	case !settings.Enabled:
		state.MonitorState = "disabled"
	default:
		c.mu.Lock()
		off := c.current.Status == pb.ProtectionStatus_PROTECTION_STATUS_OFF
		c.mu.Unlock()
		if off {
			state.MonitorState = "proxy_off"
		} else {
			readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			windows, err := c.readAlertWindows(readCtx, settings, &p.cache)
			cancel()
			if err != nil {
				state.MonitorState = "failed"
				state.ErrorCode = "traffic_alerts_read_failed"
				if err.Error() == "collection_interrupted" {
					state.MonitorState = "collection_interrupted"
					state.ErrorCode = ""
				}
			} else {
				state.MonitorState = "collecting"
				for _, w := range windows {
					if w.partial {
						state.MonitorState = "partial"
					}
				}
				p.mu.Lock()
				if p.settings.Revision != settings.Revision {
					p.mu.Unlock()
					return
				}
				next, notices, e := nextAlertEpisodes(p.runtime.Episodes, windows, operationID)
				if e == nil && !reflect.DeepEqual(next, p.runtime.Episodes) {
					runtime := config.TrafficAlertRuntime{Revision: settings.Revision, Episodes: next}
					e = p.store.SaveRuntime(ctx, runtime)
					if e == nil {
						p.runtime = runtime
					} else {
						// A failed durability barrier may follow a committed rename.
						loaded, loadErr := p.store.LoadRuntime()
						if loadErr == nil && loaded.Revision == settings.Revision {
							p.runtime = loaded
						}
						if loadErr != nil {
							p.failure = e
						}
					}
				}

				p.mu.Unlock()
				if e != nil {
					state.MonitorState = "failed"
					state.ErrorCode = "traffic_alerts_state_save_failed"
				} else {
					for _, n := range notices {
						state.Notices = append(state.Notices, alertNoticeProto(n))
					}
				}
			}
		}
	}
	if ctx.Err() != nil {
		return
	}
	// Serialize the revision check with publication so a late read cannot replace
	// the new settings value after SaveTrafficAlertSettings returns.
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.settings.Revision == settings.Revision {
		state.ActiveNoticeIds = alertActiveIDs(p.runtime)
		c.publishTrafficAlerts(state)
	}
}

var errTrafficAlertConflict = errors.New("traffic_alerts_conflict")

func alertActiveIDs(runtime config.TrafficAlertRuntime) []string {
	var ids []string
	for _, episode := range runtime.Episodes {
		ids = append(ids, episode.Latest.ID)
	}
	sort.Strings(ids)
	return ids
}
