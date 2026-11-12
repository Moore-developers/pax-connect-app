package controller

import (
	"context"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"math"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

type NetworkPrivacyRepository interface {
	Load() (config.NetworkPrivacy, bool, error)
	Save(context.Context, config.NetworkPrivacy) error
}
type privacyOwner struct {
	store   NetworkPrivacyRepository
	value   config.NetworkPrivacy
	failure error
}

// Configure before starting the sole configuration operation owner.
func (c *Controller) ConfigureNetworkPrivacy(store NetworkPrivacyRepository) {
	p, _, e := store.Load()
	c.privacy = &privacyOwner{store: store, value: p, failure: e}
}
func privacyProto(p config.NetworkPrivacy) *pb.NetworkPrivacy {
	return &pb.NetworkPrivacy{Revision: p.Revision, DnsMode: p.DNSMode, Webrtc: p.WebRTC}
}
func (c *Controller) GetNetworkPrivacy(ctx context.Context, _ *emptypb.Empty) (*pb.NetworkPrivacy, error) {
	var value *pb.NetworkPrivacy
	var failure error
	err := c.query(ctx, func(context.Context) {
		if c.privacy == nil {
			failure = errors.New("network_privacy_unavailable")
			return
		}
		loaded, _, err := c.privacy.store.Load()
		if err != nil {
			c.privacy.failure = err
			failure = err
			return
		}
		c.privacy.value = loaded
		c.privacy.failure = nil
		value = privacyProto(loaded)
	})
	if err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, status.Error(codes.FailedPrecondition, "network_privacy_unavailable")
	}
	return value, nil
}
func (c *Controller) SaveNetworkPrivacy(ctx context.Context, q *pb.NetworkPrivacy) (*pb.NetworkPrivacy, error) {
	if q == nil {
		return nil, status.Error(codes.InvalidArgument, "network_privacy_invalid")
	}
	var result *pb.NetworkPrivacy
	evidence := &config.SystemEvent{Type: "settings.network_privacy", Name: "保存网络保护", Category: config.EventCategory{Key: "settings", Name: "设置与数据", Symbol: "lock.shield"}, Result: "completed"}
	err := c.routingCommandEvidence(ctx, evidence, func(operation context.Context) error {
		p := c.privacy
		if p == nil || p.failure != nil {
			return status.Error(codes.FailedPrecondition, "network_privacy_unavailable")
		}
		if q.Revision != p.value.Revision {
			return status.Error(codes.Aborted, "network_privacy_conflict")
		}
		if p.value.Revision == math.MaxUint64 {
			return status.Error(codes.FailedPrecondition, "network_privacy_invalid")
		}
		v := config.NetworkPrivacy{Revision: p.value.Revision + 1, DNSMode: q.DnsMode, WebRTC: q.Webrtc}
		if v.Validate() != nil {
			return status.Error(codes.InvalidArgument, "network_privacy_invalid")
		}
		if err := p.store.Save(operation, v); err != nil {
			loaded, _, loadErr := p.store.Load()
			if loadErr != nil || loaded != p.value {
				p.failure = errors.New("network_privacy_unavailable")
			}
			return status.Error(codes.Internal, "network_privacy_save_failed")
		}
		p.value = v
		result = privacyProto(v)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
