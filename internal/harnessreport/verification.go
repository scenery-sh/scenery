package harnessreport

type VerificationCoverage struct {
	PayloadIdentity
	Provenance
	RunID         string             `json:"run_id"`
	InputRevision string             `json:"input_revision"`
	GeneratedAt   string             `json:"generated_at"`
	Complete      bool               `json:"complete"`
	CI            *CIContext         `json:"ci,omitempty"`
	Lanes         []VerificationLane `json:"lanes"`
}

type VerificationLane struct {
	ID         string   `json:"id"`
	Required   bool     `json:"required"`
	Selected   bool     `json:"selected"`
	Outcome    string   `json:"outcome"`
	RunID      string   `json:"run_id,omitempty"`
	Artifact   string   `json:"artifact,omitempty"`
	AgeSeconds float64  `json:"age_seconds"`
	Reason     string   `json:"reason,omitempty"`
	Command    []string `json:"command"`
	WallMS     int64    `json:"wall_ms"`
	Boundary   string   `json:"boundary"`
}
