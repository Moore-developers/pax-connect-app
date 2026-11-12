package config

import (
	"testing"
	"time"
)

func TestLogStageStatusUsesEvidenceAndKeepsFailureOnClose(t *testing.T) {
	at := time.Now()
	for _, c := range []struct {
		f    FlowFact
		want string
	}{
		{FlowFact{Purpose: "access", Phase: "connecting", Failure: "connection_timeout", ClosedAt: &at}, "建立连接：连接超时"},
		{FlowFact{Failure: "connection_timeout"}, "阶段未确认：连接超时"},
		{FlowFact{Purpose: "access", ResponseKnown: true, ClosedAt: &at}, "连接结束：此前收到回包"},
		{FlowFact{Purpose: "access", EstablishmentState: "ready", ClosedAt: &at}, "连接结束：此前通道已建立"},
		{FlowFact{Purpose: "access", ClosedAt: &at}, "连接结束：未观测到回包"},
		{FlowFact{}, "阶段未确认：等待结果"},
		{FlowFact{ClosedAt: &at}, "连接结束：历史响应未采集"},
		{FlowFact{Purpose: "dns", Failure: "dns_not_found"}, "域名解析：域名不存在"},
		{FlowFact{Purpose: "access", StateUnknown: true}, "采集中断：后续状态未知"},
	} {
		if got := LogStatus(c.f); got != c.want {
			t.Errorf("got %s want %s", got, c.want)
		}
	}
}
