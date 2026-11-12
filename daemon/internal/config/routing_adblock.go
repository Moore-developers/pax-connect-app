package config

const AdblockPackageID = "adblock"
const AdblockSource = "https://github.com/217heidai/adblockfilters/releases/download/rule/adblocksingboxlite.srs"

// CanUpdate shares subscription lifecycle for user sources and the fixed ad list.
func (p RoutingPackage) CanUpdate() bool { return p.Kind == "subscription" || p.Kind == "adblock" }
