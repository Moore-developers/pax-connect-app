// Package aiusage reads local CLI usage metadata and owns its SQLite history.
// It never retains prompts, responses or credentials, and never accesses the network.
// Explicit repricing stays durable. Verified late Codex model metadata can refine
// the same request and its buckets atomically; weak evidence never guesses a model.
// Controller consumes its bounded queries; aiprofile attribution is injected by cmd.
package aiusage
