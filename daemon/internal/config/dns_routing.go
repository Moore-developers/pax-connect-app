package config

// CompileDNSRouting projects the same ordered traffic rules onto predicates that
// are known before lookup. Application/IP-dependent rules use the mode default.
func CompileDNSRouting(library RoutingLibrary, apps []RoutingApplication, routesByID map[string]string, mode string) ([]map[string]any, error) {
	if mode == "" {
		mode = "balanced"
	}
	if mode != "balanced" && mode != "strict" {
		return nil, ErrRoutingLibrary
	}
	result := []map[string]any{}
	routes := map[string]string{}
	for id, tag := range routesByID {
		routes[id] = tag
	}
	// A user rule whose predicate is not known before lookup may win over
	// every later rule. Keep that uncertainty instead of exposing a lower
	// domestic/selected resolver as though its priority were already decided.
	var userRules []bool
	compiled, err := compileRouting(library, apps, routes, true, func(_ RoutingMatchEvidence, user bool) {
		userRules = append(userRules, user)
	})
	if err != nil {
		return nil, err
	}
	for index, rule := range compiled {
		match, ok := dnsDomainMatch(rule)
		if !ok {
			if userRules[index] && (rule["outbound"] != nil || rule["action"] == "reject") {
				result = append(result, map[string]any{"server": "dns-remote"})
				break
			}
			continue
		}
		if rule["action"] == "reject" {
			match["action"] = "reject"
		} else {
			outbound, ok := rule["outbound"].(string)
			if !ok {
				continue
			}
			tag := "dns-remote"
			if outbound == "direct" && mode == "balanced" {
				tag = "dns-domestic"
			}
			if outbound != "direct" && outbound != "proxy" {
				tag = outbound
			}
			match["server"] = tag
		}
		result = append(result, match)
	}
	return result, nil
}

// Preserve only predicates decidable from the query domain. Dropping a process/IP
// condition from an AND/NOT would widen a rule and disclose queries to the wrong pool.
func dnsDomainMatch(rule map[string]any) (map[string]any, bool) {
	match := map[string]any{}
	for key, value := range rule {
		switch key {
		case "action", "outbound":
		case "domain", "domain_suffix", "domain_keyword", "domain_regex", "type", "mode", "invert":
			match[key] = value
		case "rule_set":
			// Built-in GeoSite is domain-only. Other sets can depend on resolved IPs.
			valid := false
			switch tags := value.(type) {
			case []string:
				valid = len(tags) == 1 && (tags[0] == "geosite-cn" || tags[0] == "geosite-google")
			case []any:
				valid = len(tags) == 1 && (tags[0] == "geosite-cn" || tags[0] == "geosite-google")
			}
			if !valid {
				return nil, false
			}
			match[key] = value
		case "rules":
			children, ok := value.([]any)
			if !ok {
				return nil, false
			}
			converted := []any{}
			for _, child := range children {
				v, ok := child.(map[string]any)
				if !ok {
					return nil, false
				}
				v, ok = dnsDomainMatch(v)
				if !ok {
					return nil, false
				}
				converted = append(converted, v)
			}
			match[key] = converted
		default:
			return nil, false
		}
	}
	return match, len(match) > 0
}

// DNSRoutingRules is the shared complete query policy for activation and hot edits.
func DNSRoutingRules(l RoutingLibrary, apps []RoutingApplication, tags map[string]string, mode string) ([]map[string]any, error) {
	rules, err := CompileDNSRouting(l, apps, tags, mode)
	if err != nil {
		return nil, err
	}
	return append([]map[string]any{{"domain_suffix": []string{"local", "lan", "home.arpa"}, "server": "dns-intranet"}}, rules...), nil
}
