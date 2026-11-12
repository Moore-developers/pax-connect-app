// Package stats reduces credential-free native cumulative counters into product usage.
// Document.Accept owns sequence deduplication, route watermarks, UTC five-minute
// buckets and local calendar days. It performs no I/O and imports no upstream
// types. Controller serializes it and persists immutable bounded JSON via storage.
package stats
