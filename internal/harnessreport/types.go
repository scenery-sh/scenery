package harnessreport

import (
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/machine"
)

type PayloadIdentity = machine.PayloadIdentity

type Diagnostic struct {
	Stage           string `json:"stage"`
	Severity        string `json:"severity"`
	File            string `json:"file,omitempty"`
	Line            int    `json:"line,omitempty"`
	Column          int    `json:"column,omitempty"`
	Message         string `json:"message"`
	SuggestedAction string `json:"suggested_action,omitempty"`
}

type Artifact struct {
	Name           string `json:"name"`
	Path           string `json:"path"`
	Kind           string `json:"kind,omitempty"`
	SchemaRevision string `json:"schema_revision,omitempty"`
	Exists         bool   `json:"exists"`
}

type ArtifactHygieneReport struct {
	ForbiddenTracked []string `json:"forbidden_tracked"`
	WorkspaceRules   []string `json:"workspace_rules"`
}

type CLIContractCommand struct {
	Name  string `json:"name"`
	Usage bool   `json:"usage"`
	Smoke bool   `json:"smoke"`
	Mode  string `json:"mode"`
	Error string `json:"error,omitempty"`
}

type CLIContractReport struct {
	Commands []CLIContractCommand `json:"commands"`
}

type ChangedAreaReport struct {
	PayloadIdentity
	ChangedFiles        []ChangedFile `json:"changed_files"`
	IgnoredFiles        []ChangedFile `json:"ignored_files,omitempty"`
	AffectedPackages    []string      `json:"affected_packages"`
	ValidationClasses   []string      `json:"validation_classes"`
	RecommendedCommands []string      `json:"recommended_commands"`
	RelevantDocs        []string      `json:"relevant_docs"`
	RiskFlags           []string      `json:"risk_flags"`
	Diagnostics         []Diagnostic  `json:"diagnostics,omitempty"`
}

type ChangedFile struct {
	Path     string `json:"path"`
	Status   string `json:"status"`
	Category string `json:"category"`
	Package  string `json:"package,omitempty"`
}

type DriftReport struct {
	PayloadIdentity
	CLI         CLIContractReport     `json:"cli"`
	Env         EnvVarReport          `json:"env"`
	Artifacts   ArtifactHygieneReport `json:"artifacts"`
	Embeds      EmbedReport           `json:"embeds"`
	Diagnostics []Diagnostic          `json:"diagnostics,omitempty"`
}

type EmbedFinding struct {
	File                     string   `json:"file"`
	Pattern                  string   `json:"pattern"`
	Resolved                 []string `json:"resolved"`
	CoveredByBinaryFreshness bool     `json:"covered_by_binary_freshness"`
}

type EmbedReport struct {
	Embeds []EmbedFinding `json:"embeds"`
}

type EnvValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type EnvVarFinding struct {
	Name         string                `json:"name"`
	Scope        string                `json:"scope"`
	UsedInCode   bool                  `json:"used_in_code"`
	Documented   bool                  `json:"documented"`
	Registered   bool                  `json:"registered"`
	RegistryName string                `json:"registry_name,omitempty"`
	Direction    string                `json:"direction,omitempty"`
	Stability    string                `json:"stability,omitempty"`
	Category     string                `json:"category,omitempty"`
	Secret       bool                  `json:"secret,omitempty"`
	Files        []string              `json:"files,omitempty"`
	References   []envpolicy.Reference `json:"references,omitempty"`
	Violations   []string              `json:"violations,omitempty"`
}

type EnvVarReport struct {
	Variables []EnvVarFinding `json:"variables"`
	Registry  string          `json:"registry,omitempty"`
}

type Evidence struct {
	PayloadIdentity
	Command      []string           `json:"command,omitempty"`
	CWD          string             `json:"cwd,omitempty"`
	StartedAt    string             `json:"started_at,omitempty"`
	DurationMS   int64              `json:"duration_ms"`
	ExitCode     *int               `json:"exit_code,omitempty"`
	StdoutTail   string             `json:"stdout_tail,omitempty"`
	StderrTail   string             `json:"stderr_tail,omitempty"`
	Artifacts    []EvidenceArtifact `json:"artifacts,omitempty"`
	ReproCommand string             `json:"repro_command,omitempty"`
}

type EvidenceArtifact struct {
	Name           string `json:"name"`
	Path           string `json:"path"`
	Kind           string `json:"kind,omitempty"`
	SchemaRevision string `json:"schema_revision,omitempty"`
}

type FixtureMatrixReport struct {
	PayloadIdentity
	Fixtures    []FixtureResult `json:"fixtures"`
	Diagnostics []Diagnostic    `json:"diagnostics,omitempty"`
}

type FixtureResult struct {
	Name        string          `json:"name"`
	Path        string          `json:"path"`
	Check       bool            `json:"check"`
	Inspect     map[string]bool `json:"inspect"`
	Diagnostics []Diagnostic    `json:"diagnostics,omitempty"`
}

type Knowledge struct {
	Entrypoints []KnowledgeFile `json:"entrypoints"`
	Schemas     []KnowledgeFile `json:"schemas"`
}

type KnowledgeFile struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

type PackageTiming struct {
	Package         string       `json:"package"`
	Seconds         float64      `json:"seconds"`
	BudgetSeconds   float64      `json:"budget_seconds,omitempty"`
	IsolatedSeconds *float64     `json:"isolated_seconds,omitempty"`
	Tests           []TestTiming `json:"slow_tests,omitempty"`
}

type SchemaValidationItem struct {
	Name   string `json:"name"`
	Schema string `json:"schema"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

type SchemaValidationReport struct {
	PayloadIdentity
	Validated   []SchemaValidationItem `json:"validated"`
	Diagnostics []Diagnostic           `json:"diagnostics,omitempty"`
}

type SelfArchitectureSummary struct {
	BlockingCount           int      `json:"blocking_count"`
	WarningCount            int      `json:"warning_count"`
	ChangedAreaWarningCount int      `json:"changed_area_warning_count"`
	DebtWarningCount        int      `json:"debt_warning_count"`
	LargeFileCount          int      `json:"large_file_count"`
	TopChangedWarnings      []string `json:"top_changed_warnings,omitempty"`
	Artifact                string   `json:"artifact"`
}

type SelfAttentionItem struct {
	Severity     string   `json:"severity"`
	Category     string   `json:"category"`
	Message      string   `json:"message"`
	NextAction   string   `json:"next_action,omitempty"`
	TopEntries   []string `json:"top_entries,omitempty"`
	OmittedCount int      `json:"omitted_count,omitempty"`
	Artifact     string   `json:"artifact,omitempty"`
	Drilldown    string   `json:"drilldown,omitempty"`
}

type SelfDriftSummary struct {
	EnvVarCount     int    `json:"env_var_count"`
	Diagnostics     int    `json:"diagnostics"`
	CLICommandCount int    `json:"cli_command_count"`
	EmbedCount      int    `json:"embed_count"`
	Artifact        string `json:"artifact"`
}

type SelfFixtureMatrixSummary struct {
	PassCount int    `json:"pass_count"`
	FailCount int    `json:"fail_count"`
	Artifact  string `json:"artifact"`
}

type SelfKnowledgeSummary struct {
	EntrypointCount int      `json:"entrypoint_count"`
	SchemaCount     int      `json:"schema_count"`
	ReviewDueCount  int      `json:"review_due_count"`
	StaleCount      int      `json:"stale_count"`
	TopReviewDue    []string `json:"top_review_due,omitempty"`
	Drilldown       string   `json:"drilldown"`
}

type SelfRepo struct {
	Root       string `json:"root"`
	ModulePath string `json:"module_path"`
	GoModPath  string `json:"go_mod_path"`
}

type SelfResponse struct {
	PayloadIdentity
	OK               bool                    `json:"ok"`
	GeneratedAt      string                  `json:"generated_at"`
	Mode             string                  `json:"mode"`
	Repo             SelfRepo                `json:"repo"`
	Knowledge        Knowledge               `json:"knowledge"`
	Toolchain        *ToolchainReport        `json:"toolchain,omitempty"`
	ChangedArea      *ChangedAreaReport      `json:"changed_area,omitempty"`
	Drift            *DriftReport            `json:"drift,omitempty"`
	TestTiming       *TestTimingReport       `json:"test_timing,omitempty"`
	FixtureMatrix    *FixtureMatrixReport    `json:"fixture_matrix,omitempty"`
	SchemaValidation *SchemaValidationReport `json:"schema_validation,omitempty"`
	Steps            []Step                  `json:"steps"`
	Artifacts        []Artifact              `json:"artifacts"`
	NextActions      []string                `json:"next_actions,omitempty"`
	Wrote            string                  `json:"wrote,omitempty"`
}

type SelfSchemaValidationSummary struct {
	PassCount int    `json:"pass_count"`
	FailCount int    `json:"fail_count"`
	Artifact  string `json:"artifact"`
}

type SelfSummaryChanges struct {
	ChangedFiles     []ChangedFile `json:"changed_files"`
	ChangedFileCount int           `json:"changed_file_count"`
	IgnoredFiles     []ChangedFile `json:"ignored_files,omitempty"`
	IgnoredFileCount int           `json:"ignored_file_count,omitempty"`
	AffectedPackages []string      `json:"affected_packages,omitempty"`
	RiskFlags        []string      `json:"risk_flags,omitempty"`
	Recommended      []string      `json:"recommended_commands,omitempty"`
	RelevantDocs     []string      `json:"relevant_docs,omitempty"`
	OmittedFileCount int           `json:"omitted_file_count,omitempty"`
}

type SelfSummaryRepo struct {
	Root       string `json:"root"`
	ModulePath string `json:"module_path"`
	GoModPath  string `json:"go_mod_path"`
}

type SelfSummaryReports struct {
	Drift            *SelfDriftSummary            `json:"drift,omitempty"`
	TestTiming       *SelfTestTimingSummary       `json:"test_timing,omitempty"`
	Knowledge        SelfKnowledgeSummary         `json:"knowledge"`
	Architecture     SelfArchitectureSummary      `json:"architecture"`
	SchemaValidation *SelfSchemaValidationSummary `json:"schema_validation,omitempty"`
	FixtureMatrix    *SelfFixtureMatrixSummary    `json:"fixture_matrix,omitempty"`
}

type SelfSummaryResponse struct {
	PayloadIdentity
	OK                bool                `json:"ok"`
	Status            string              `json:"status"`
	GeneratedAt       string              `json:"generated_at"`
	Mode              string              `json:"mode"`
	Repo              SelfSummaryRepo     `json:"repo"`
	CanProceed        bool                `json:"can_proceed"`
	ChangedArea       SelfSummaryChanges  `json:"changed_area"`
	DiagnosticSummary map[string]int      `json:"diagnostic_summary"`
	Attention         []SelfAttentionItem `json:"attention,omitempty"`
	Steps             []SelfSummaryStep   `json:"steps"`
	Reports           SelfSummaryReports  `json:"reports"`
	Artifacts         []Artifact          `json:"artifacts"`
	Drilldowns        []string            `json:"drilldowns"`
	Wrote             string              `json:"wrote,omitempty"`
}

type SelfSummaryStep struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Status       string         `json:"status"`
	DurationMS   int64          `json:"duration_ms"`
	ErrorCount   int            `json:"error_count"`
	WarningCount int            `json:"warning_count"`
	Summary      map[string]any `json:"summary,omitempty"`
	Diagnostics  []Diagnostic   `json:"diagnostics,omitempty"`
	OutputTail   string         `json:"output_tail,omitempty"`
	Artifacts    []Artifact     `json:"artifacts,omitempty"`
}

type SelfTestTimingSummary struct {
	Lane                  string          `json:"lane,omitempty"`
	TotalSeconds          IntOrFloat      `json:"total_seconds"`
	ConfirmationSeconds   IntOrFloat      `json:"confirmation_seconds,omitempty"`
	BudgetSeconds         IntOrFloat      `json:"budget_seconds"`
	TargetSeconds         IntOrFloat      `json:"target_seconds,omitempty"`
	PackageCount          int             `json:"package_count"`
	TestPackageCount      int             `json:"test_package_count,omitempty"`
	BuiltCount            int             `json:"built_count,omitempty"`
	ObservedSlowTestCount int             `json:"observed_slow_test_count"`
	SlowTestCount         int             `json:"slow_test_count"`
	WarningCount          int             `json:"warning_count"`
	TopSlowTests          []TestTiming    `json:"top_slow_tests,omitempty"`
	TopSlowPackages       []PackageTiming `json:"top_slow_packages,omitempty"`
	Artifact              string          `json:"artifact"`
}

type Step struct {
	Name        string         `json:"name"`
	Command     []string       `json:"command"`
	OK          bool           `json:"ok"`
	DurationMS  int64          `json:"duration_ms"`
	Evidence    *Evidence      `json:"evidence,omitempty"`
	Effects     []string       `json:"effects,omitempty"`
	Summary     map[string]any `json:"summary,omitempty"`
	Diagnostics []Diagnostic   `json:"diagnostics,omitempty"`
	Error       string         `json:"error,omitempty"`
	OutputTail  string         `json:"output_tail,omitempty"`
}

type TestBinaryBuild struct {
	Package string  `json:"package"`
	BuildID string  `json:"build_id"`
	Seconds float64 `json:"seconds"`
}

// harnessTestBinaryTiming attributes the fresh lane's pre-execution cost.
// Package listing and per-binary linking happen before any test runs, so they
// never appear in Go's per-package elapsed times; without this breakdown a
// cold-run penalty cannot be traced to the links that caused it.
type TestBinaryTiming struct {
	ManifestHit           bool              `json:"manifest_hit"`
	PrepareSeconds        float64           `json:"prepare_seconds"`
	ListSeconds           float64           `json:"list_seconds,omitempty"`
	AggregateBuildSeconds float64           `json:"aggregate_build_seconds,omitempty"`
	BuildParallelism      int               `json:"build_parallelism"`
	BuiltCount            int               `json:"built_count"`
	TestPackageCount      int               `json:"test_package_count"`
	Builds                []TestBinaryBuild `json:"builds,omitempty"`
}

type TestTiming struct {
	Name                 string    `json:"name"`
	Package              string    `json:"package"`
	Class                string    `json:"class"`
	Seconds              float64   `json:"seconds"`
	TargetSeconds        float64   `json:"target_seconds"`
	BudgetSeconds        float64   `json:"budget_seconds"`
	IsolatedSamples      []float64 `json:"isolated_samples,omitempty"`
	IsolatedP95          *float64  `json:"isolated_p95_seconds,omitempty"`
	ClassificationReason string    `json:"classification_reason,omitempty"`
}

type TestTimingBudgets struct {
	Lane                   string                `json:"lane,omitempty"`
	TargetSeconds          float64               `json:"target_seconds,omitempty"`
	TotalSeconds           float64               `json:"total_seconds"`
	PackageSeconds         float64               `json:"package_seconds"`
	PackageOverrides       map[string]float64    `json:"package_overrides,omitempty"`
	DefaultTestClass       string                `json:"default_test_class"`
	TestTargetSeconds      float64               `json:"test_target_seconds"`
	TestSeconds            float64               `json:"test_seconds"`
	IntegrationExceptions  []TestTimingException `json:"integration_exceptions"`
	ConfirmationRuns       int                   `json:"confirmation_runs,omitempty"`
	ConfirmationPercentile int                   `json:"confirmation_percentile,omitempty"`
	// ConfirmationScope is "regressions" (confirm only candidates that are new
	// or materially worse than the last recorded run) or "all" (confirm every
	// candidate). Everyday fresh runs use the former; the release audit lane
	// uses the latter.
	ConfirmationScope string `json:"confirmation_scope,omitempty"`
	// TestBinaryCount is the maximum number of packages that produce a test
	// binary. Fresh runs compare it to test_binaries.test_package_count on
	// every run, including warm ones, because adding a package is a cold-link
	// cost even when this run reused cached binaries.
	TestBinaryCount int `json:"test_binary_count,omitempty"`
	// ColdPrepareSeconds is the maximum preparation wall time at the recorded
	// build parallelism. It applies only when built_count equals
	// test_package_count, i.e. a full cold prepare. Partial rebuilds are not
	// compared against this number.
	ColdPrepareSeconds float64 `json:"cold_prepare_seconds,omitempty"`
	Mode               string  `json:"mode"`
}

// harnessTestTimingException preserves the published timing-report shape.
// The policy must remain empty: external-boundary proof belongs in the release
// harness, never in a top-level Go test root.
type TestTimingException struct {
	Package        string  `json:"package"`
	Name           string  `json:"name"`
	Class          string  `json:"class"`
	TargetSeconds  float64 `json:"target_seconds"`
	BudgetSeconds  float64 `json:"budget_seconds"`
	BoundaryReason string  `json:"classification_reason"`
}

type TestTimingReport struct {
	PayloadIdentity
	Command                  []string          `json:"command"`
	Env                      []string          `json:"env,omitempty"`
	TotalSeconds             float64           `json:"total_seconds"`
	ConfirmationSeconds      float64           `json:"confirmation_seconds,omitempty"`
	TestBinaries             *TestBinaryTiming `json:"test_binaries,omitempty"`
	Packages                 []PackageTiming   `json:"packages"`
	ObservedSlowTests        []TestTiming      `json:"observed_slow_tests,omitempty"`
	ObservedIntegrationTests []TestTiming      `json:"observed_integration_tests,omitempty"`
	SlowTests                []TestTiming      `json:"slow_tests,omitempty"`
	DeferredConfirmations    []TimingDeferral  `json:"deferred_confirmations,omitempty"`
	Budgets                  TestTimingBudgets `json:"budgets"`
	Diagnostics              []Diagnostic      `json:"diagnostics,omitempty"`
}

// harnessTimingDeferral records a candidate the confirmation pass did not
// re-run. Confirmation costs multiple isolated executions per candidate, so
// the regression scope skips outliers already at their known level; recording
// each skip keeps the report from reading as "nothing was over budget".
type TimingDeferral struct {
	Package         string  `json:"package"`
	Name            string  `json:"name,omitempty"`
	Seconds         float64 `json:"seconds"`
	BaselineSeconds float64 `json:"baseline_seconds"`
	Reason          string  `json:"reason"`
}

type ToolchainReport struct {
	PayloadIdentity
	Tools       []ToolchainTool `json:"tools"`
	Env         []EnvValue      `json:"env,omitempty"`
	Diagnostics []Diagnostic    `json:"diagnostics,omitempty"`
}

type ToolchainTool struct {
	Name      string `json:"name"`
	Scope     string `json:"scope"`
	Required  bool   `json:"required"`
	Present   bool   `json:"present"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
	Commit    string `json:"commit,omitempty"`
	BuiltAt   string `json:"built_at,omitempty"`
	GoVersion string `json:"go_version,omitempty"`
	Error     string `json:"error,omitempty"`
}

// intOrFloat preserves compact numeric JSON while keeping the summary structs simple.
type IntOrFloat float64
