package controller

import (
	"strings"

	"pax-connect/daemon/internal/config"
)

// projectStoredIdentity corrects only identities whose saved object is evidence.
// Aggregated search text may describe several flows and never supplies ownership.
func projectStoredIdentity(i aggregateIdentity) aggregateIdentity {
	if i.Dimension == "application" || i.Dimension == "system" {
		if strings.HasPrefix(i.Object, "process:") {
			path := strings.TrimPrefix(i.Object, "process:")
			source := config.ClassifyTrafficSource(config.FlowFact{Process: path, ApplicationID: i.Object})
			i.Kind, i.Object, i.Name = source.Kind, source.ID, source.Name
			switch source.Kind {
			case "system", "tool":
				i.Dimension = "system"
			case "app":
				i.Dimension = "application"
			default:
				i.Dimension = "unknown"
			}
		}
	}
	if i.Dimension == "domain" {
		if i.Object == "unknown" {
			i.Name = "历史未细分目标（域名未记录）"
		}
		if i.Object == "target:ip-only" {
			i.Name = "仅记录 IP（域名未识别）"
		}
		if i.Object == "target:unknown" {
			i.Name = "目标未采集"
		}
	}
	return i
}

func storedSource(i aggregateIdentity) config.TrafficSource {
	if strings.HasPrefix(i.Object, "process:") {
		return config.ClassifyTrafficSource(config.FlowFact{Process: strings.TrimPrefix(i.Object, "process:"), ApplicationID: i.Object})
	}
	return config.TrafficSource{Kind: i.Kind, Name: i.Name, Label: i.Name}
}
