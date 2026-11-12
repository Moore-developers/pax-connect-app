package controller

import (
	"context"
	"errors"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"testing"
)

type memoryPrivacy struct {
	value config.NetworkPrivacy
	fail  bool
}

func (m *memoryPrivacy) Load() (config.NetworkPrivacy, bool, error) { return m.value, true, nil }
func (m *memoryPrivacy) Save(_ context.Context, v config.NetworkPrivacy) error {
	if m.fail {
		return errors.New("write_failed")
	}
	m.value = v
	return nil
}
func TestNetworkPrivacyCASAndSaveFailureDoNotChangeIntent(t *testing.T) {
	c, _ := routingController(t)
	repo := &memoryPrivacy{value: config.DefaultNetworkPrivacy()}
	c.ConfigureNetworkPrivacy(repo)
	ctx := context.Background()
	initial, e := c.GetNetworkPrivacy(ctx, nil)
	if e != nil || initial.DnsMode != "balanced" || initial.Webrtc {
		t.Fatal(initial, e)
	}
	value, e := c.SaveNetworkPrivacy(ctx, &pb.NetworkPrivacy{Revision: 1, DnsMode: "strict", Webrtc: true})
	if e != nil || value.Revision != 2 {
		t.Fatal(value, e)
	}
	if _, e = c.SaveNetworkPrivacy(ctx, &pb.NetworkPrivacy{Revision: 1, DnsMode: "balanced"}); e == nil {
		t.Fatal("stale settings accepted")
	}
	repo.fail = true
	if _, e = c.SaveNetworkPrivacy(ctx, &pb.NetworkPrivacy{Revision: 2, DnsMode: "balanced"}); e == nil {
		t.Fatal("save failure hidden")
	}
	saved, e := c.GetNetworkPrivacy(ctx, nil)
	if e != nil || saved.DnsMode != "strict" || !saved.Webrtc {
		t.Fatal("failed save mutated saved policy", saved, e)
	}
}

// An atomic rename can succeed before the durability barrier reports failure.
type uncertainPrivacy struct{ memoryPrivacy }

func (m *uncertainPrivacy) Save(_ context.Context, v config.NetworkPrivacy) error {
	m.value = v
	return errors.New("directory_sync_failed")
}
func TestNetworkPrivacyRereadRecoversUncertainDurability(t *testing.T) {
	c, _ := routingController(t)
	repo := &uncertainPrivacy{memoryPrivacy{value: config.DefaultNetworkPrivacy()}}
	c.ConfigureNetworkPrivacy(repo)
	if _, err := c.SaveNetworkPrivacy(context.Background(), &pb.NetworkPrivacy{Revision: 1, DnsMode: "strict"}); err == nil {
		t.Fatal("uncertain save reported success")
	}
	current, err := c.GetNetworkPrivacy(context.Background(), nil)
	if err != nil || current.Revision != 2 || current.DnsMode != "strict" {
		t.Fatal("reread did not reconcile durable revision", current, err)
	}
	if c.privacy.failure != nil {
		t.Fatal("valid reread did not clear failure")
	}
}
