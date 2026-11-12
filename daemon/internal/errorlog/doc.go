// Package errorlog owns the bounded private operational storage error sink.
// It accepts only library/code/duration, never product observations or diagnostic text.
// Dependencies: cmd → errorlog → storage. No controller, proto or sing-box dependency.
package errorlog
