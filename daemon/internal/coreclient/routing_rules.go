package coreclient

import (
	"context"
	"encoding/json"
	"fmt"
	"google.golang.org/protobuf/types/known/emptypb"
	"pax-connect/daemon/internal/config"
	"slices"
	"strings"
	"time"
)

// ReadRoutingRules reads a sanitized projection of the last committed profile,
// after authenticating that its owned core is running. It never applies a profile.
func (s *Session) ReadRoutingRules(ctx context.Context) (config.RoutingRulesSnapshot, error) {
	out := config.RoutingRulesSnapshot{State: "not_running", At: time.Now()}
	if s.lastProfile == "" {
		return out, nil
	}
	client, closer, state, err := s.open(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = closer.Close() }()
	if state != "STARTED" {
		return out, nil
	}
	hello, err := client.Hello(ctx, &emptypb.Empty{})
	if err != nil {
		return out, ErrUnavailable
	}
	if hello.RuntimeSession != s.rulesRuntimeSession || (hello.SelectedTag != "" && hello.SelectedTag != s.selected) {
		return config.RoutingRulesSnapshot{State: "unknown", At: time.Now()}, nil
	}

	var document struct {
		Route struct {
			Rules []struct {
				Action, Outbound string
				IPIsPrivate      bool `json:"ip_is_private"`
				IPVersion        int  `json:"ip_version"`
				Protocol         ruleStrings
				RuleSet          ruleStrings `json:"rule_set"`
			}
			Final string
		}
	}
	if len(s.lastProfile) > 4<<20 || json.Unmarshal([]byte(s.lastProfile), &document) != nil {
		return out, ErrUnavailable
	}
	for index, r := range document.Route.Rules {
		match, source := "其它内核匹配规则", "内核配置"
		switch {
		case r.Action == "sniff":
			match = "识别连接协议和目标域名"
		case slices.Contains(r.Protocol, "dns"):
			match = "DNS 查询"
		case r.IPVersion == 6:
			match = "IPv6 流量"
		case r.IPIsPrivate:
			match = "私有网络地址"
		case len(r.RuleSet) > 0:
			var sets []string
			for _, tag := range r.RuleSet {
				switch tag {
				case "geosite-google":
					sets = append(sets, "Google 服务（GeoSite Google）")
				case "geosite-cn":
					sets = append(sets, "中国域名（GeoSite CN）")
				case "geoip-cn":
					sets = append(sets, "中国 IP（GeoIP CN）")
				default:
					sets = append(sets, "内核规则集")
				}
			}
			match = strings.Join(sets, "、")
			source = "内置 Geo 规则集"
		}
		action := "代理"
		switch r.Action {
		case "sniff":
			action = "识别协议"
		case "hijack-dns":
			action = "交由 Pax 处理"
		case "reject":
			action = "拦截"
		default:
			if r.Outbound == "direct" {
				action = "直连"
			}
		}
		out.Rules = append(out.Rules, config.RoutingRule{ID: fmt.Sprintf("kernel-rule-%d", index+1), Order: uint32(index + 1), Match: match, Action: action, Source: source})
	}
	if document.Route.Final != "" {
		action := "当前线路"
		if document.Route.Final == "direct" {
			action = "直连"
		}
		out.Rules = append(out.Rules, config.RoutingRule{ID: "kernel-default", Order: uint32(len(out.Rules) + 1), Match: "未命中上述规则的其它目标", Action: action, Source: "无"})
	}
	out.State = "active"
	return out, nil
}

type ruleStrings []string

func (v *ruleStrings) UnmarshalJSON(raw []byte) error {
	var single string
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &single); err != nil {
			return err
		}
		*v = []string{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return err
	}
	*v = many
	return nil
}
