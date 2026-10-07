package harnessreport

// TestResults keeps runner case durations separate from command wall time.
// Missing per-case timestamps and lifecycle stages remain explicitly unknown.
type TestResults struct {
	PayloadIdentity
	Provenance
	Context          MeasurementContext `json:"context"`
	Outcome          string             `json:"outcome"`
	SelectedPackages []string           `json:"selected_packages,omitempty"`
	Filter           string             `json:"filter,omitempty"`
	RunID            string             `json:"run_id"`
	ParentRunID      string             `json:"parent_run_id"`
	StepID           string             `json:"step_id"`
	SourceCommit     string             `json:"source_commit"`
	InputRevision    string             `json:"input_revision"`
	Purpose          string             `json:"purpose"`
	Lane             string             `json:"lane"`
	Runner           string             `json:"runner"`
	RunnerVersion    string             `json:"runner_version"`
	OS               string             `json:"os"`
	Architecture     string             `json:"architecture"`
	Command          []string           `json:"command"`
	CWD              string             `json:"cwd"`
	SelectedFiles    []string           `json:"selected_files"`
	WallSeconds      float64            `json:"wall_seconds"`
	Attempt          int                `json:"attempt"`
	Completeness     CaseCompleteness   `json:"completeness"`
	Cases            []TestCaseResult   `json:"cases"`
	Artifacts        []EvidenceArtifact `json:"artifacts"`
	UnknownStages    []string           `json:"unknown_stages"`
}

type CaseCompleteness struct {
	Complete     bool     `json:"complete"`
	Expected     int      `json:"expected"`
	Discovered   int      `json:"discovered"`
	Executed     int      `json:"executed"`
	Terminal     int      `json:"terminal"`
	ParserErrors []string `json:"parser_errors"`
	MissingFiles []string `json:"missing_files"`
}

type TestCaseResult struct {
	ID                  string  `json:"id"`
	ParentID            string  `json:"parent_id,omitempty"`
	File                string  `json:"file"`
	Suite               string  `json:"suite"`
	Name                string  `json:"name"`
	Outcome             string  `json:"outcome"`
	Seconds             float64 `json:"seconds"`
	Attempt             int     `json:"attempt"`
	FirstAttemptOutcome string  `json:"first_attempt_outcome"`
	Boundary            string  `json:"boundary"`
	Replayed            bool    `json:"replayed"`
}

// MeasurementContext identifies a comparable execution cohort. Source revisions
// belong to the run identity; changing source is the subject of comparison.
type MeasurementContext struct {
	Environment        string     `json:"environment"`
	Host               string     `json:"host"`
	OS                 string     `json:"os"`
	Architecture       string     `json:"architecture"`
	Runner             string     `json:"runner"`
	RunnerVersion      string     `json:"runner_version"`
	CPUs               int        `json:"cpus"`
	PackageParallelism int        `json:"package_parallelism"`
	BuildParallelism   int        `json:"build_parallelism"`
	Workload           string     `json:"workload"`
	ResultCache        string     `json:"result_cache"`
	CI                 *CIContext `json:"ci,omitempty"`
}

type CIContext struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Workflow   string `json:"workflow"`
	Job        string `json:"job"`
	Event      string `json:"event"`
	RunID      string `json:"run_id"`
	Attempt    int    `json:"attempt"`
	URL        string `json:"url"`
}
