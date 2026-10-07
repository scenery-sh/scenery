// Package feature coordinates local feature worktrees and validated main landing.
package feature

import "time"

const PolicyFile = "scenery.features.json"

type Record struct {
	Version      int      `json:"version"`
	Name         string   `json:"name"`
	Purpose      string   `json:"purpose"`
	Path         string   `json:"path"`
	Branch       string   `json:"branch"`
	Base         string   `json:"base"`
	Stage        string   `json:"stage"`
	Dependencies []string `json:"dependencies"`
	Closed       bool     `json:"closed"`
}

type Row struct {
	Record
	Head           string    `json:"head"`
	Status         string    `json:"status"`
	Outstanding    []string  `json:"outstanding"`
	Dirty          []string  `json:"dirty"`
	Overlap        []Overlap `json:"overlap"`
	Blockers       []string  `json:"blockers"`
	Validation     string    `json:"validation"`
	Landing        string    `json:"landing"`
	Runtime        string    `json:"runtime"`
	LastCheckpoint string    `json:"last_checkpoint,omitempty"`
	LastLanding    string    `json:"last_landing,omitempty"`
}

type Overlap struct {
	Feature string   `json:"feature"`
	Paths   []string `json:"paths"`
}

type Checkpoint struct {
	Feature          string `json:"feature"`
	Commit           string `json:"commit"`
	IntegratedCommit string `json:"integrated_commit,omitempty"`
}

type Check struct {
	ID        string   `json:"id"`
	Command   string   `json:"command"`
	Args      []string `json:"args"`
	Expensive bool     `json:"expensive,omitempty"`
	Reuse     bool     `json:"reuse,omitempty"`
	WhenPaths []string `json:"when_paths,omitempty"`
}

type Policy struct {
	Version     int     `json:"version"`
	ProbeLimit  int     `json:"probe_limit"`
	Development []Check `json:"development"`
	Landing     []Check `json:"landing"`
}

type CheckReceipt struct {
	ID          string    `json:"id"`
	Revision    string    `json:"revision"`
	Environment string    `json:"environment"`
	Command     []string  `json:"command"`
	CWD         string    `json:"cwd"`
	StartedAt   time.Time `json:"started_at"`
	DurationMS  int64     `json:"duration_ms"`
	ExitCode    int       `json:"exit_code"`
	Outcome     string    `json:"outcome"`
	Reused      bool      `json:"reused"`
	Archive     string    `json:"archive"`
}

type Candidate struct {
	Version     int            `json:"version"`
	ID          string         `json:"id"`
	Path        string         `json:"path"`
	Base        string         `json:"base"`
	Remote      string         `json:"remote"`
	Checkpoints []Checkpoint   `json:"checkpoints"`
	Next        int            `json:"next"`
	Tip         string         `json:"tip"`
	Tree        string         `json:"tree"`
	Revision    string         `json:"revision"`
	Status      string         `json:"status"`
	Conflicts   []string       `json:"conflicts"`
	Changed     []string       `json:"changed"`
	Diff        string         `json:"diff"`
	Checks      []Check        `json:"checks"`
	Receipts    []CheckReceipt `json:"receipts"`
	CreatedAt   time.Time      `json:"created_at"`
	PublishedAt *time.Time     `json:"published_at,omitempty"`
}

type Overview struct {
	RepoRoot   string      `json:"repo_root"`
	Main       string      `json:"main"`
	Features   []Row       `json:"features"`
	Candidates []Candidate `json:"candidates"`
}

type Update struct {
	Purpose         string
	Stage           string
	Dependencies    []string
	SetDependencies bool
}

// DevelopmentEvidence reports focused feedback for exactly one authored snapshot.
type DevelopmentEvidence struct {
	Revision string         `json:"revision"`
	Passed   bool           `json:"passed"`
	Receipts []CheckReceipt `json:"receipts"`
}
