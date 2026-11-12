package controller

import (
	"context"
	"pax-connect/daemon/internal/config"
)

// Test convenience wrapper follows the production context-aware identity path.
func identifyHistoryApplication(f *config.FlowFact) {
	identifyHistoryApplicationContext(context.Background(), f)
}
