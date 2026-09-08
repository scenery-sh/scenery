package repoinfo

import (
	"scenery.sh/internal/harnessreport"
	"scenery.sh/internal/machine"
)

type PayloadIdentity = machine.PayloadIdentity

type PackageInfo struct {
	ImportPath string
	Dir        string
	RelDir     string
}

type KnowledgeIndex struct {
	Kind            string              `json:"kind"`
	SchemaRevision  string              `json:"schema_revision"`
	GeneratedAt     string              `json:"generated_at"`
	OwnerDefault    string              `json:"owner_default"`
	FreshnessPolicy FreshnessPolicy     `json:"freshness_policy"`
	Documents       []KnowledgeDocument `json:"documents"`
	Plans           IndexPlans          `json:"plans"`
	TechDebt        string              `json:"tech_debt"`
}

type FreshnessPolicy struct {
	DefaultReviewDays int      `json:"default_review_days"`
	QualityGrades     []string `json:"quality_grades"`
	FreshnessStates   []string `json:"freshness_states"`
}

type IndexPlans struct {
	Active    string `json:"active"`
	Completed string `json:"completed"`
}

type KnowledgeDocument struct {
	Path         string   `json:"path"`
	Title        string   `json:"title"`
	Owner        string   `json:"owner"`
	Status       string   `json:"status"`
	Quality      string   `json:"quality"`
	Freshness    string   `json:"freshness"`
	LastReviewed string   `json:"last_reviewed"`
	ReviewAfter  string   `json:"review_after"`
	Summary      string   `json:"summary"`
	Tags         []string `json:"tags"`
	SchemaRefs   []string `json:"schema_refs,omitempty"`
}

type Options struct {
	ForPath   string
	Tag       string
	Status    string
	ReviewDue bool
	All       bool
}

type Query struct {
	Mode      string `json:"mode"`
	ForPath   string `json:"for_path,omitempty"`
	Tag       string `json:"tag,omitempty"`
	Status    string `json:"status,omitempty"`
	ReviewDue bool   `json:"review_due,omitempty"`
	All       bool   `json:"all,omitempty"`
}

type Response struct {
	PayloadIdentity
	Repo                 harnessreport.SelfRepo `json:"repo"`
	Query                Query                  `json:"query"`
	Summary              Summary                `json:"summary"`
	Warnings             []string               `json:"warnings,omitempty"`
	Agents               Agents                 `json:"agents"`
	Documents            []Document             `json:"documents"`
	VerificationCommands []string               `json:"verification_commands,omitempty"`
	Plans                *Plans                 `json:"plans,omitempty"`
	TechDebt             *ArtifactRef           `json:"tech_debt,omitempty"`
}

type Summary struct {
	DocumentCount               int            `json:"document_count"`
	SelectedDocumentCount       int            `json:"selected_document_count"`
	MissingCount                int            `json:"missing_count"`
	ReviewDueCount              int            `json:"review_due_count"`
	StaleCount                  int            `json:"stale_count"`
	AgentScopeCount             int            `json:"agent_scope_count"`
	StaleChildIndexEntryCount   int            `json:"stale_child_index_entry_count"`
	MissingChildIndexEntryCount int            `json:"missing_child_index_entry_count"`
	Quality                     map[string]int `json:"quality"`
}

type Plans struct {
	Active    ArtifactRef `json:"active"`
	Completed ArtifactRef `json:"completed"`
}

type ArtifactRef struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

type Agents struct {
	Scopes                   []AgentScope `json:"scopes"`
	ChildIndexPath           string       `json:"child_index_path,omitempty"`
	ChildIndexEntries        []string     `json:"child_index_entries,omitempty"`
	StaleChildIndexEntries   []string     `json:"stale_child_index_entries,omitempty"`
	MissingChildIndexEntries []string     `json:"missing_child_index_entries,omitempty"`
}

type AgentScope struct {
	Path  string `json:"path"`
	Scope string `json:"scope"`
}

type Document struct {
	KnowledgeDocument
	Exists     bool      `json:"exists"`
	SizeBytes  int64     `json:"size_bytes,omitempty"`
	ModifiedAt string    `json:"modified_at,omitempty"`
	ReviewDue  bool      `json:"review_due"`
	Stale      bool      `json:"stale"`
	Role       string    `json:"role,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	Sections   []Section `json:"sections,omitempty"`
}

type Section struct {
	Heading   string `json:"heading"`
	Anchor    string `json:"anchor"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}
