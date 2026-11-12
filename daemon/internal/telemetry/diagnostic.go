package telemetry

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
)

// Diagnostic contains only stage codes, booleans and timings; never addresses or credentials.
type Diagnostic struct {
	OperationID string    `json:"operation_id,omitempty"`
	Phase       string    `json:"phase,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	Route       string    `json:"route,omitempty"`
	Time        time.Time `json:"time"`
	Stage       string    `json:"stage"`
	Code        string    `json:"code"`
	DurationMS  int64     `json:"duration_ms"`
	BudgetMS    int64     `json:"remaining_budget_ms"`
	Success     bool      `json:"success"`
	Observed    bool      `json:"observed"`
}

var diagnostics struct {
	sync.Mutex
	path string
}

// ConfigureDiagnostics selects a private file owned by the composition layer.
func ConfigureDiagnostics(path string) {
	diagnostics.Lock()
	defer diagnostics.Unlock()
	diagnostics.path = path
}

// RecordDiagnostic appends a bounded, allowlisted observation. Logging cannot fail an operation.
func RecordDiagnostic(event Diagnostic) {
	diagnostics.Lock()
	defer diagnostics.Unlock()
	if diagnostics.path == "" {
		return
	}
	event.Time = time.Now().UTC()
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	if info, err := os.Lstat(diagnostics.path); err == nil && info.Mode().IsRegular() && info.Size() >= 1<<20 {
		if os.Rename(diagnostics.path, diagnostics.path+".previous") != nil {
			return
		}
	}
	file, err := os.OpenFile(diagnostics.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return
	}
	if file.Chmod(0600) != nil {
		return
	}
	_, _ = file.Write(append(data, '\n'))
}

// RouteToken correlates local route records without exposing names, addresses or credentials.
func RouteToken(id string) string {
	if id == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(id))
	return fmt.Sprintf("%x", digest[:6])
}
