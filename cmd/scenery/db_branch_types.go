package main

import (
	"time"

	inspectdata "scenery.sh/internal/inspect"
)

const (
	dbBranchRegistrySchemaVersion = "scenery.db.branch.registry.v2"
	dbBranchStatusSchemaVersion   = "scenery.db.branch.status.v1"
	dbBranchListSchemaVersion     = "scenery.db.branch.list.v1"
	dbBranchPinSchemaVersion      = "scenery.db.branch.v1"
	dbBranchDefaultParentBranch   = "main"
	dbBranchDefaultPolicy         = "worktree"
	dbBranchDefaultNameTemplate   = "{app}/{git_branch}"
	dbBranchDefaultTTL            = "168h"
	dbBranchDefaultDatabase       = "postgres"
	dbBranchDefaultRole           = "scenery"
)

type dbBranchOptions struct {
	Command string
	AppRoot string
	JSON    bool
	Branch  string
	Yes     bool
	Force   bool
	At      string
	After   string
	Older   string
	Target  string
}

type worktreeDBPin struct {
	SchemaVersion string `json:"schema_version"`
	Provider      string `json:"provider"`
	Project       string `json:"project"`
	ParentBranch  string `json:"parent_branch"`
	Branch        string `json:"branch"`
	BranchID      string `json:"branch_id"`
	Database      string `json:"database"`
	Role          string `json:"role"`
	SessionID     string `json:"session_id,omitempty"`
	WorktreeRoot  string `json:"worktree_root,omitempty"`
	CreatedBy     string `json:"created_by"`
	TTL           string `json:"ttl,omitempty"`
}

type dbBranchRegistry struct {
	SchemaVersion string          `json:"schema_version"`
	Provider      string          `json:"provider"`
	UpdatedAt     string          `json:"updated_at,omitempty"`
	Leases        []dbBranchLease `json:"leases"`
}

type dbBranchLease struct {
	Pin       worktreeDBPin     `json:"pin"`
	Status    string            `json:"status"`
	Endpoint  *dbBranchEndpoint `json:"endpoint,omitempty"`
	CreatedAt string            `json:"created_at,omitempty"`
	UpdatedAt string            `json:"updated_at,omitempty"`
	ExpiresAt string            `json:"expires_at,omitempty"`
}

type dbBranchEndpoint struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	Role     string `json:"role"`
	SSLMode  string `json:"sslmode,omitempty"`
	Source   string `json:"source,omitempty"`
}

type dbBranchStatusResult struct {
	SchemaVersion  string             `json:"schema_version"`
	OK             bool               `json:"ok"`
	App            inspectdata.AppRef `json:"app"`
	Provider       string             `json:"provider"`
	Status         string             `json:"status"`
	BackendStatus  string             `json:"backend_status,omitempty"`
	BackendMessage string             `json:"backend_message,omitempty"`
	Connection     *dbBranchEndpoint  `json:"connection,omitempty"`
	PinPath        string             `json:"pin_path"`
	Pin            *worktreeDBPin     `json:"pin,omitempty"`
	DatabaseURLEnv string             `json:"database_url_env"`
	PSQLCommand    string             `json:"psql_command"`
	ResetCommand   string             `json:"reset_command"`
	Message        string             `json:"message,omitempty"`
}

type dbBranchListResult struct {
	SchemaVersion string              `json:"schema_version"`
	OK            bool                `json:"ok"`
	App           inspectdata.AppRef  `json:"app"`
	Provider      string              `json:"provider"`
	Branches      []worktreeDBPin     `json:"branches"`
	Leases        []dbBranchListLease `json:"leases,omitempty"`
	RegistryPath  string              `json:"registry_path,omitempty"`
	Message       string              `json:"message,omitempty"`
}

type dbBranchListLease struct {
	Pin       worktreeDBPin     `json:"pin"`
	Status    string            `json:"status"`
	Endpoint  *dbBranchEndpoint `json:"endpoint,omitempty"`
	CreatedAt string            `json:"created_at,omitempty"`
	UpdatedAt string            `json:"updated_at,omitempty"`
	ExpiresAt string            `json:"expires_at,omitempty"`
}

type dbBranchResolution struct {
	Pin           worktreeDBPin
	Source        string
	Created       bool
	BackendStatus dbBranchBackendStatus
}

type dbBranchBackendStatus struct {
	Status   string
	Message  string
	Endpoint *dbBranchEndpoint
}

type dbBranchConnectionInfo struct {
	DatabaseURL  string
	DatabaseName string
	Endpoint     dbBranchEndpoint
}

type dbBranchRestorePoint struct {
	Ref          string    `json:"ref"`
	Source       string    `json:"source"`
	Branch       string    `json:"branch"`
	BranchID     string    `json:"branch_id"`
	Project      string    `json:"project"`
	DatabaseName string    `json:"database_name"`
	CreatedAt    time.Time `json:"created_at"`
}
