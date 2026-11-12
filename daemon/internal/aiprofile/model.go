package aiprofile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"
)

// CLI identifies a supported configuration format, not a running process.
type CLI string

const (
	Codex  CLI = "codex"
	Claude CLI = "claude"
	DSH    CLI = "dsh"
)

var clients = []CLI{Codex, Claude, DSH}

// Failure is a controlled product cause; it must never contain native configuration text.
type Failure string

func (f Failure) Error() string { return string(f) }

const (
	Invalid          Failure = "ai_profile_invalid"
	Conflict         Failure = "ai_profile_conflict"
	InUse            Failure = "ai_profile_in_use"
	Unsupported      Failure = "ai_template_unsupported"
	Changed          Failure = "ai_target_changed"
	Unsafe           Failure = "ai_target_unsafe"
	Expired          Failure = "ai_plan_expired"
	StoreInvalid     Failure = "ai_store_invalid"
	Uncertain        Failure = "ai_commit_uncertain"
	RecoveryRequired Failure = "ai_recovery_required"
)

// Profile is a secret-free projection. ConnectionRevision ignores changes to Pax-only metadata.
type Profile struct {
	ID                 string  `json:"id"`
	Revision           uint64  `json:"revision"`
	ConnectionRevision uint64  `json:"connection_revision"`
	Name               string  `json:"name"`
	BaseURL            string  `json:"base_url"`
	Multiplier         float64 `json:"multiplier"`
	// ModelMultipliers overrides Multiplier for a request's model id; an absent model inherits.
	ModelMultipliers map[string]float64 `json:"model_multipliers,omitempty"`
	ModelIDs         []string           `json:"model_ids"`
	Kind             string             `json:"kind,omitempty"`
	NativeCLI        CLI                `json:"native_cli,omitempty"`
	Catalog          []ModelInfo        `json:"catalog,omitempty"`
	ModelsState      string             `json:"models_state,omitempty"`
	ModelsEndpoint   string             `json:"models_endpoint,omitempty"`
	ModelsChecked    int64              `json:"models_checked,omitempty"`
	ModelsError      string             `json:"models_error,omitempty"`
	HasKey           bool               `json:"has_key"`
}
type savedProfile struct {
	MetadataAt int64 `json:"metadata_at,omitempty"`
	Schema     int   `json:"schema"`
	Profile
	Generated   map[CLI]generatedSet `json:"generated,omitempty"`
	Key         string               `json:"api_key"`
	OperationID string               `json:"operation_id"`
}

// Binding reports files on disk, never that an existing CLI session adopted them.
type Binding struct {
	ServiceSource    string `json:"service_source"`
	CLI              CLI    `json:"cli"`
	ProfileID        string `json:"profile_id"`
	State            string `json:"state"`
	Reason           string `json:"reason"`
	InspectionID     string `json:"inspection_id"`
	TemplateRevision uint64 `json:"template_revision"`
	Endpoint         string `json:"endpoint"`
	TargetPath       string `json:"target_path"`
	RequiresModel    bool   `json:"requires_model"`
}

// Workspace contains no credentials or native document bytes.
type Workspace struct {
	Profiles []Profile `json:"profiles"`
	Bindings []Binding `json:"bindings"`
}

// Mutation only changes Pax-owned saved state, never native CLI targets.
type Mutation struct {
	Action           string
	OperationID      string
	ExpectedRevision uint64
	Profile          Profile
	Key              string
	CLI              CLI
	InspectionID     string
}

// PlanRequest freezes a Profile and the observed native files before application.
type PlanRequest struct {
	CLI              CLI
	ProfileID        string
	Revision         uint64
	TemplateRevision uint64
	InspectionID     string
	Restore          bool
	Initialize       bool
	ModelID          string
}

// Plan exposes the selected Profile and URL without credentials.
type Plan struct {
	ID          string    `json:"id"`
	CLI         CLI       `json:"cli"`
	ProfileID   string    `json:"profile_id"`
	BaseURL     string    `json:"base_url"`
	ExpiresAt   time.Time `json:"expires_at"`
	Initializes bool      `json:"initializes"`
	TargetPath  string    `json:"target_path"`
}

// ApplyRequest confirms provider-only edits to the observed CLI files.
type ApplyRequest struct{ PlanID, OperationID string }

var identifier = regexp.MustCompile(`^(?:[a-fA-F0-9]{32}|[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12})$`)

func validMetadata(p Profile) bool {
	return identifier.MatchString(p.ID) && strings.TrimSpace(p.Name) != "" && len(p.Name) <= 256 && validMultiplier(p.Multiplier) && len(p.ModelIDs) <= 1000 && validModelMultipliers(p.ModelMultipliers, false)
}

// validMultiplier is the stored-value rule: finite and within 0..100.
func validMultiplier(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 100
}

// threeDecimals reports whether v has at most three decimal places.
func threeDecimals(v float64) bool { return math.Abs(v*1000-math.Round(v*1000)) < 1e-6 }

// validModelMultipliers checks keys and values. strict adds the three-decimal rule that
// new input must meet; values saved earlier are only range-checked so they still load.
func validModelMultipliers(m map[string]float64, strict bool) bool {
	if len(m) > 1000 {
		return false
	}
	for k, v := range m {
		if k == "" || len(k) > 256 || !validMultiplier(v) || strict && !threeDecimals(v) {
			return false
		}
	}
	return true
}

// validInputMultipliers is the rule for a value the user is saving now.
func validInputMultipliers(p Profile) bool {
	return threeDecimals(p.Multiplier) && validModelMultipliers(p.ModelMultipliers, true)
}
func validProfile(p Profile, key string) bool {
	return validMetadata(p) && validEndpoint(p.BaseURL) && key != "" && len(key) <= 16384
}

func newID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic("system randomness unavailable")
	}
	return hex.EncodeToString(b[:])
}
func cause(err error) error {
	if err == nil {
		return nil
	}
	var f Failure
	if errors.As(err, &f) {
		return f
	}
	return StoreInvalid
}

func sameProfileInput(a, b Profile) bool {
	a.Revision = 0
	a.ConnectionRevision = 0
	a.HasKey = false
	a.Catalog = nil
	a.ModelIDs = nil
	a.ModelsState = ""
	a.ModelsEndpoint = ""
	a.ModelsChecked = 0
	a.ModelsError = ""
	b.Revision = 0
	b.ConnectionRevision = 0
	b.HasKey = false
	b.Catalog = nil
	b.ModelIDs = nil
	b.ModelsState = ""
	b.ModelsEndpoint = ""
	b.ModelsChecked = 0
	b.ModelsError = ""
	return reflect.DeepEqual(a, b)
}

func validEndpoint(value string) bool {
	u, err := url.Parse(value)
	return len(value) <= 8192 && err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}
