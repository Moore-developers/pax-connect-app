package measurement

import "time"

const (
	// DefaultParallel is the initial lightweight-probe admission budget.
	DefaultParallel = 20
	// HardParallel is the product-wide safety ceiling, not a default.
	HardParallel     = 100
	LatencyTimeout   = 20 * time.Second
	ExitTimeout      = 10 * time.Second
	ExitQueueTimeout = 30 * time.Second
	DownloadTimeout  = 30 * time.Second
	// TotalMeasureTimeout is retained for legacy CF queries; route latency uses LatencyTimeout.
	TotalMeasureTimeout = 51 * time.Second
	MaxDownloadBatch    = 20
)

// Capacity is a hardware admission budget, not a promise about remote networks.
type Capacity struct {
	Initial   int
	Ceiling   int
	HardLimit int
}

// Recommended gives lightweight tests a CPU/memory-based budget with a minimum
// of twenty; download concurrency is configured separately.
func Recommended(cores int, memoryBytes uint64) Capacity {
	ceiling := cores * 4
	if memoryBytes != 0 {
		memoryLimit := int(memoryBytes/(1024*1024*1024)) * 4
		if memoryLimit < ceiling {
			ceiling = memoryLimit
		}
	}
	if ceiling < DefaultParallel {
		ceiling = DefaultParallel
	}
	if ceiling > HardParallel {
		ceiling = HardParallel
	}
	return Capacity{Initial: DefaultParallel, Ceiling: ceiling, HardLimit: HardParallel}
}
