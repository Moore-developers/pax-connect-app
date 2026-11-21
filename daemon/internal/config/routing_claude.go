package config

const ClaudePackageID = "builtin:claude"
const ClaudePackageName = "Claude"

func NewClaudeRoutingPackage() RoutingPackage {
	return RoutingPackage{ID: ClaudePackageID, Name: ClaudePackageName, Kind: "claude", Destination: "global", Version: "随 App 提供"}
}

// RulesForMatching returns the bundled Claude list or the saved entries for other packages.
func (p RoutingPackage) RulesForMatching() []RoutingEntry {
	if p.Kind != "claude" {
		return p.Rules
	}
	suffixes := []string{
		"anthropic.com", "claude.ai", "claude.com", "claude.app", "claudeusercontent.com", "claudemcpcontent.com",
		"google.com", "googleapis.com", "gstatic.com", "gmail.com", "googleusercontent.com",
		"stripe.com", "stripe.network", "stripecdn.com", "withpersona.com", "hcaptcha.com", "recaptcha.net",
		"intercomcdn.com", "siftscience.com", "segment.io",
	}
	hosts := []string{
		"clau.de", "challenges.cloudflare.com", "api-iam.intercom.io", "nexus-websocket-a.intercom.io",
		"us5.datadoghq.com", "http-intake.logs.us5.datadoghq.com", "browser-intake-us5-datadoghq.com", "us.sentry.io",
	}
	rules := make([]RoutingEntry, 0, len(suffixes)+len(hosts))
	for _, host := range suffixes {
		rules = append(rules, claudeDomainRule("domain_suffix", host))
	}
	for _, host := range hosts {
		rules = append(rules, claudeDomainRule("domain", host))
	}
	return rules
}

func claudeDomainRule(method, host string) RoutingEntry {
	return RoutingEntry{ID: "claude:" + host, Method: method, Target: host, Destination: "global", Enabled: true}
}

// BuiltIn identifies packages whose position and identity are fixed by Pax.
func (p RoutingPackage) BuiltIn() bool {
	return p.Kind == "geo" || p.Kind == "adblock" || p.Kind == "claude"
}

// BuiltInStart is the first slot reserved for the fixed packages.
func (l RoutingLibrary) BuiltInStart() int {
	for index, p := range l.Packages {
		if p.BuiltIn() {
			return index
		}
	}
	return len(l.Packages)
}
