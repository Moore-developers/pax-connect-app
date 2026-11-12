package config

import "time"

// RouteMeasurement contains independent latency and exit facts, without bodies.
type RouteMeasurement struct {
	ExitLocation, LastExitLocation  *ExitLocation
	Duration                        time.Duration
	Stable                          time.Duration
	StableSamples                   uint32
	StableError, TestTarget         string
	ExitIP                          string
	ExitError                       string
	Cold, Second                    time.Duration
	Reused                          bool
	Method, FirstError, SecondError string
}

// DownloadMeasurement is one complete bounded transfer, independent of latency/exit.
type DownloadMeasurement struct {
	Bytes          uint64
	Duration       time.Duration
	BytesPerSecond float64
	Setup          time.Duration
	End            string
}
