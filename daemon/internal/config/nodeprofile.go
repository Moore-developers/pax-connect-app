package config

// NodeProfile is the public, credential-free product route model.
type NodeProfile struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ProtocolName string `json:"protocol"`
	Server       string `json:"server"`
	Port         uint16 `json:"port"`
}

// DebugProfile contains one local private route; never serialize it to a product event.
type DebugProfile = RouteConfig
