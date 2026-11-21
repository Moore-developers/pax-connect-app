package controller

import (
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/telemetry"
	"strings"
)

// Static product labels only. This never parses user names or sentences into state.
var eventLabelCodes = map[string]string{
	"开启代理":       "event.proxy.enable",
	"关闭代理":       "event.proxy.disable",
	"切换线路":       "event.route.switch",
	"线路操作":       "event.route.manage",
	"编辑线路":       "event.route.edit",
	"保存CF线路":     "event.route.save_cf",
	"订阅刷新":       "event.subscription.refresh",
	"规则操作":       "event.routing.change",
	"线路测试":       "event.measurement.route",
	"下载测速":       "event.measurement.download",
	"整组测试":       "event.measurement.batch",
	"并发评估":       "event.measurement.parallel",
	"网络检测":       "event.measurement.network",
	"日志设置":       "event.settings.history",
	"证书设置":       "event.settings.certificate",
	"保存网络保护":     "event.settings.privacy",
	"修改日志保留期":    "event.settings.retention",
	"清除日志历史":     "event.settings.clear",
	"验证当前通道":     "event.verification.request",
	"网络环境变化":     "event.network.change",
	"代理启停":       "event.category.power",
	"线路切换":       "event.category.routes",
	"线路管理":       "event.category.management",
	"订阅更新":       "event.category.subscriptions",
	"分流规则":       "event.category.routing",
	"检测与测速":      "event.category.measurement",
	"设置与数据":      "event.category.settings",
	"通道验证":       "event.category.verification",
	"其它系统事件":     "event.category.operations",
	"网络与后台":      "event.category.network",
	"域名解析":       "event.category.dns",
	"保存与应用":      "event.section.persistence",
	"当次下载测试":     "event.section.download",
	"当次变更":       "event.section.changes",
	"当次检查结果":     "event.section.checks",
	"当次线路测试":     "event.section.routes",
	"当次结果":       "event.section.results",
	"新设置":        "event.section.settings",
	"更新对象":       "event.section.updated_objects",
	"更新结果":       "event.section.update_result",
	"检查对象":       "event.section.checked_objects",
	"清除结果":       "event.section.clear",
	"线路变化":       "event.section.route_changes",
	"证书策略设置":     "event.section.certificate",
	"IPQuery 风险": "event.field.ipquery_risk",
	"保留":         "event.field.retained",
	"保留期":        "event.field.retention",
	"处理阶段":       "event.field.stage",
	"新增":         "event.field.added",
	"新策略":        "event.field.policy",
	"更新":         "event.field.updated",
	"样本时间":       "event.field.sample_time",
	"正文完整":       "event.field.body_complete",
	"版本":         "event.field.version",
	"生效范围":       "event.field.scope",
	"移除":         "event.field.removed",
	"线路":         "event.field.route",
	"结果":         "event.field.result",
	"结论":         "event.field.conclusion",
	"范围":         "event.field.range",
	"解析有效线路":     "event.field.valid_routes",
	"触发来源":       "event.field.trigger",
	"订阅":         "event.field.subscription",
	"请求耗时":       "event.field.duration",
	"速度":         "event.field.speed",
	"配置版本":       "event.field.config_version",
	"首次耗时":       "event.field.first_latency",
	"日志及原文；不保存被删除内容": "event.value.logs_cleared",
	"[已省略敏感内容]":      "event.value.sensitive_omitted",
}

func staticEventText(value string) *config.ProductText {
	if code := eventLabelCodes[value]; code != "" {
		return &config.ProductText{Code: code}
	}
	return nil
}
func addEventTextSemantics(event *config.SystemEvent) {
	if event == nil {
		return
	}
	if event.NameText == nil {
		event.NameText = staticEventText(event.Name)
	}
	if event.Category.NameText == nil {
		event.Category.NameText = staticEventText(event.Category.Name)
	}
	for i := range event.Sections {
		section := &event.Sections[i]
		if section.TitleText == nil {
			section.TitleText = staticEventText(section.Title)
		}
		for j := range section.Fields {
			field := &section.Fields[j]
			if field.LabelText == nil {
				field.LabelText = staticEventText(field.Label)
			}
			// Values and before/after may be user names. Only the redacted marker is fixed content.
			if field.Value == "[已省略敏感内容]" {
				field.ValueText = staticEventText(field.Value)
			}
		}
	}
}

func safeProductText(text *config.ProductText) *config.ProductText {
	if text == nil {
		return nil
	}
	result := &config.ProductText{Code: text.Code}
	if text.Params != nil {
		result.Params = map[string]string{}
		for key, value := range text.Params {
			lower := strings.ToLower(key)
			sensitive := false
			for _, word := range []string{"password", "token", "secret", "authorization", "private_key"} {
				sensitive = sensitive || strings.Contains(lower, word)
			}
			if sensitive {
				value = "[已省略敏感内容]"
			}
			result.Params[key] = telemetry.ScrubEventText(value)
		}
	}
	return result
}
