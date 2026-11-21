package controller

import (
	pb "pax-connect/daemon/gen/proto/paxv1"
)

func routingMessageText(value string) *pb.ProductText {
	code := map[string]string{
		"规则变更仅影响新连接，已建立的连接保持。":          "routing_message.changes_new_connections",
		"本地规则已保存；当前为直连，开启代理后生效。":        "routing_message.offline_saved",
		"已保存；当前为直连，开启代理后生效。":            "routing_message.saved_offline",
		"已保存并应用，仅影响新连接；已有连接保持。":         "routing_message.saved_active",
		"Geo 基础包已更新；旧连接保持，新连接使用更新后的规则。": "routing_message.geo_updated",
		"自动更新未完成，保留上一有效版本。":             "routing_message.auto_failed",
	}[value]
	if code == "" {
		return nil
	}
	return &pb.ProductText{Code: code}
}
