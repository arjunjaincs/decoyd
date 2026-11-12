// Package attribution defines the types used to describe the process and
// package that accessed a deployed canary-token file.
//
// Attribution data is collected by the fanotify event loop (Tier 1 only) and
// attached to TriggerEvent records in the triglog. It is never collected for
// inotify-only (Tier 2) events.
//
// All string fields are pre-sanitized (ANSI, bidi, C0/C1 stripped) before
// being stored. Token values are never stored here (O8).
package attribution

// Confidence describes how complete the attribution data is.
//
//	ConfidenceLow    – PID read but /proc/<pid>/ vanished before stat could complete.
//	ConfidenceMedium – exe resolved, ppid chain read; no SHA-256.
//	ConfidenceHigh   – exe read and SHA-256 computed (file ≤100 MB, no error).
type Confidence uint8

const (
	ConfidenceLow    Confidence = 1
	ConfidenceMedium Confidence = 2
	ConfidenceHigh   Confidence = 3
)

// String returns a human-readable label for the confidence level.
func (c Confidence) String() string {
	switch c {
	case ConfidenceLow:
		return "low"
	case ConfidenceMedium:
		return "medium"
	case ConfidenceHigh:
		return "high"
	default:
		return "unknown"
	}
}

// ProcessInfo holds attribution data for one process.
// All string fields are sanitized before storage (ANSI, bidi, C0/C1 stripped).
type ProcessInfo struct {
	PID        int32  `json:"pid"`
	PPID       int32  `json:"ppid"`
	Comm       string `json:"comm"`                  // /proc/<pid>/comm, 15-byte max
	Exe        string `json:"exe"`                   // readlink /proc/<pid>/exe
	ExeDeleted bool   `json:"exe_deleted,omitempty"` // true when exe had " (deleted)" suffix
	Cmdline    string `json:"cmdline"`               // NUL→space, capped 4096 chars
	Cwd        string `json:"cwd"`                   // readlink /proc/<pid>/cwd
	UID        uint32 `json:"uid"`                   // from /proc/<pid>/status
	StartTicks uint64 `json:"start_ticks"`           // field 22 of /proc/<pid>/stat
	ExeSHA256  string `json:"exe_sha256,omitempty"`  // hex; empty if >100 MB or deleted
}

// AncestorChain is an ordered slice of ProcessInfo records for the PPID chain.
// Index 0 is the immediate parent (PPID), index 1 is the grandparent, etc.
// Maximum depth is 8 (O7). The chain stops early if a PID is unreachable.
type AncestorChain []ProcessInfo

// Attribution is the full attribution record attached to a Tier 1 event.
// It satisfies O7 and O8: no token values are stored; only the three allowed
// npm environment variables are captured.
type Attribution struct {
	// Accessor is the process that opened/accessed the token file.
	Accessor ProcessInfo `json:"accessor"`

	// Ancestors is the PPID chain (depth ≤ 8). May be empty when the
	// parent PID is 1 (init) or when attribution confidence is Low.
	Ancestors AncestorChain `json:"ancestors,omitempty"`

	// Confidence describes completeness of the attribution data.
	Confidence Confidence `json:"confidence"`

	// npm-related environment variables (O7: only these three are captured).
	// Empty when the process is not an npm lifecycle hook.
	NpmPackage        string `json:"npm_package,omitempty"`
	NpmVersion        string `json:"npm_version,omitempty"`
	NpmLifecycleEvent string `json:"npm_lifecycle_event,omitempty"`
}

// PackageConfidence describes how certain the package attribution is.
type PackageConfidence uint8

const (
	PkgConfidenceNone   PackageConfidence = 0
	PkgConfidenceLow    PackageConfidence = 1 // pip/pip3/python ancestor with install or setup.py
	PkgConfidenceMedium PackageConfidence = 2 // cwd inside node_modules; npm/pnpm/yarn/node ancestor
	PkgConfidenceHigh   PackageConfidence = 3 // npm_lifecycle_event in {preinstall,install,postinstall,prepare}
)

// String returns a human-readable label for the package confidence level.
func (c PackageConfidence) String() string {
	switch c {
	case PkgConfidenceLow:
		return "low"
	case PkgConfidenceMedium:
		return "medium"
	case PkgConfidenceHigh:
		return "high"
	default:
		return "none"
	}
}

// PackageAttribution is the result of package-level culprit attribution (T4).
// A nil value means no package could be identified.
type PackageAttribution struct {
	// Culprit is a human-readable string like
	// "likely culprit: lodash@4.17.21 (npm install hook, high confidence)"
	Culprit string `json:"culprit"`

	// Name and Version are the package coordinates.
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`

	// Context describes how the package was identified
	// (e.g. "npm install hook", "cwd inside node_modules/lodash").
	Context string `json:"context,omitempty"`

	// Confidence in the culprit identification.
	Confidence PackageConfidence `json:"confidence"`

	// Note documents known limitations:
	// "orphaned grandchildren break the chain" when deep ancestor resolution fails.
	Note string `json:"note,omitempty"`
}
