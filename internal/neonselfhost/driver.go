package neonselfhost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pbrazdil/onlava/internal/envpolicy"
)

const (
	CapabilitiesSchemaVersion = "onlava.db.neon.driver.capabilities.v1"
	StatusSchemaVersion       = "onlava.db.neon.driver.status.v1"
	BackendSchemaVersion      = "onlava.db.neon.selfhost.backend.v1"
	DriverVersion             = "dev"
	DriverRootEnv             = "ONLAVA_NEON_SELFHOST_ROOT"
)

type Capabilities struct {
	SchemaVersion string   `json:"schema_version"`
	Provider      string   `json:"provider"`
	Driver        string   `json:"driver"`
	Version       string   `json:"version"`
	Status        string   `json:"status"`
	Actions       []string `json:"actions"`
	Capabilities  []string `json:"capabilities"`
	Message       string   `json:"message,omitempty"`
}

type Status struct {
	SchemaVersion string                `json:"schema_version"`
	Provider      string                `json:"provider"`
	Driver        string                `json:"driver"`
	Version       string                `json:"version"`
	Status        string                `json:"status"`
	Message       string                `json:"message,omitempty"`
	Root          string                `json:"root,omitempty"`
	Backend       *backendStatusSummary `json:"backend,omitempty"`
}

type BranchActionResult struct {
	Status   string          `json:"status,omitempty"`
	Message  string          `json:"message,omitempty"`
	Diff     string          `json:"diff,omitempty"`
	Endpoint *BranchEndpoint `json:"endpoint,omitempty"`
}

type BranchEndpoint struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database,omitempty"`
	Role     string `json:"role,omitempty"`
	SSLMode  string `json:"sslmode,omitempty"`
	Source   string `json:"source,omitempty"`
}

type branchActionOptions struct {
	JSON         bool
	Root         string
	Project      string
	ParentBranch string
	Branch       string
	BranchID     string
	Database     string
	Role         string
	TTL          string
	At           string
	Target       string
}

type backendStatusSummary struct {
	SchemaVersion string `json:"schema_version"`
	Present       bool   `json:"present"`
	TenantID      string `json:"tenant_id,omitempty"`
	BranchCount   int    `json:"branch_count"`
	ComputeCount  int    `json:"compute_count"`
	Message       string `json:"message,omitempty"`
}

func DefaultCapabilities() Capabilities {
	return Capabilities{
		SchemaVersion: CapabilitiesSchemaVersion,
		Provider:      "neon-selfhost",
		Driver:        "neon-selfhost-driver",
		Version:       DriverVersion,
		Status:        "ready",
		Actions:       []string{"capabilities", "status", "ensure", "reset", "restore", "delete", "diff"},
		Capabilities:  []string{"toolchain-source-build", "backend-state", "pageserver-tenant-timeline-bootstrap", "docker-compute-startup", "postgres-readiness", "postgres-database-setup", "stateful-branch-mutations", "schema-diff", "recorded-compute-readiness"},
		Message:       "driver command is installed and supports tenant/timeline bootstrap, branch compute startup, Postgres readiness, branch mutations, and schema diff",
	}
}

func DefaultStatus() Status {
	return Status{
		SchemaVersion: StatusSchemaVersion,
		Provider:      "neon-selfhost",
		Driver:        "neon-selfhost-driver",
		Version:       DriverVersion,
		Status:        "ready",
		Message:       "driver command is installed; backend readiness is reported per branch and in the backend summary",
	}
}

func Run(stdout, stderr io.Writer, args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	command := strings.TrimSpace(args[0])
	switch command {
	case "capabilities":
		jsonMode, err := parseJSONFlag(args[1:])
		if err != nil {
			return err
		}
		return writePayload(stdout, jsonMode, DefaultCapabilities(), "neon-selfhost-driver ready: capabilities,status,ensure,reset,restore,delete,diff")
	case "status":
		statusOpts, err := parseStatusFlags(args[1:])
		if err != nil {
			return err
		}
		status := DefaultStatus()
		if root, summary, err := inspectBackendStatus(statusOpts.Root); err != nil {
			status.Root = root
			status.Backend = &summary
			status.Status = "degraded"
			status.Message = err.Error()
		} else {
			status.Root = root
			status.Backend = &summary
		}
		return writePayload(stdout, statusOpts.JSON, status, "neon-selfhost-driver "+status.Status)
	case "ensure":
		opts, err := parseBranchActionFlags(args[1:])
		if err != nil {
			return err
		}
		result, err := ensurePendingBranch(opts)
		if err != nil {
			return err
		}
		return writePayload(stdout, opts.JSON, result, result.Message)
	case "reset":
		opts, err := parseBranchActionFlags(args[1:])
		if err != nil {
			return err
		}
		result, err := resetPendingBranch(opts)
		if err != nil {
			return err
		}
		return writePayload(stdout, opts.JSON, result, result.Message)
	case "restore":
		opts, err := parseBranchActionFlags(args[1:])
		if err != nil {
			return err
		}
		result, err := restorePendingBranch(opts)
		if err != nil {
			return err
		}
		return writePayload(stdout, opts.JSON, result, result.Message)
	case "delete":
		opts, err := parseBranchActionFlags(args[1:])
		if err != nil {
			return err
		}
		result, err := deleteBackendBranch(opts)
		if err != nil {
			return err
		}
		return writePayload(stdout, opts.JSON, result, result.Message)
	case "diff":
		opts, err := parseBranchActionFlags(args[1:])
		if err != nil {
			return err
		}
		result, err := diffReadyBranches(opts)
		if err != nil {
			return err
		}
		return writePayload(stdout, opts.JSON, result, "neon-selfhost-driver diff complete")
	default:
		return usageError()
	}
}

func parseBranchActionFlags(args []string) (branchActionOptions, error) {
	var opts branchActionOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			opts.JSON = true
		case "--project", "--parent-branch", "--branch", "--branch-id", "--database", "--role", "--ttl", "--at", "--target", "--root":
			flag := args[i]
			i++
			if i >= len(args) {
				return branchActionOptions{}, fmt.Errorf("missing value for %s", flag)
			}
			if strings.TrimSpace(args[i]) == "" {
				return branchActionOptions{}, fmt.Errorf("empty value for %s", flag)
			}
			switch flag {
			case "--project":
				opts.Project = args[i]
			case "--parent-branch":
				opts.ParentBranch = args[i]
			case "--branch":
				opts.Branch = args[i]
			case "--branch-id":
				opts.BranchID = args[i]
			case "--database":
				opts.Database = args[i]
			case "--role":
				opts.Role = args[i]
			case "--ttl":
				opts.TTL = args[i]
			case "--at":
				opts.At = args[i]
			case "--target":
				opts.Target = args[i]
			case "--root":
				opts.Root = args[i]
			}
		default:
			return branchActionOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	if strings.TrimSpace(opts.Project) == "" {
		return branchActionOptions{}, fmt.Errorf("missing value for --project")
	}
	if strings.TrimSpace(opts.Branch) == "" {
		return branchActionOptions{}, fmt.Errorf("missing value for --branch")
	}
	if strings.TrimSpace(opts.BranchID) == "" {
		return branchActionOptions{}, fmt.Errorf("missing value for --branch-id")
	}
	if strings.TrimSpace(opts.Database) == "" {
		opts.Database = "postgres"
	}
	if strings.TrimSpace(opts.Role) == "" {
		opts.Role = "cloud_admin"
	}
	return opts, nil
}

func parseStatusFlags(args []string) (branchActionOptions, error) {
	var opts branchActionOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			opts.JSON = true
		case "--root":
			i++
			if i >= len(args) {
				return branchActionOptions{}, fmt.Errorf("missing value for --root")
			}
			if strings.TrimSpace(args[i]) == "" {
				return branchActionOptions{}, fmt.Errorf("empty value for --root")
			}
			opts.Root = args[i]
		default:
			return branchActionOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func parseJSONFlag(args []string) (bool, error) {
	var jsonMode bool
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonMode = true
		default:
			return false, fmt.Errorf("unknown flag %q", arg)
		}
	}
	return jsonMode, nil
}

func writePayload(stdout io.Writer, jsonMode bool, payload any, text string) error {
	if !jsonMode {
		_, err := fmt.Fprintln(stdout, text)
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func usageError() error {
	return fmt.Errorf("usage: onlava-neon-selfhost-driver capabilities|status|ensure|reset|restore|diff|delete --json")
}

func ensurePendingBranch(opts branchActionOptions) (BranchActionResult, error) {
	root, err := substrateRoot(opts.Root)
	if err != nil {
		return BranchActionResult{}, err
	}
	path := filepath.Join(root, "backend.json")
	state, ok, err := ReadBackendState(path)
	if err != nil {
		return BranchActionResult{}, err
	}
	if !ok {
		state = NewBackendState("", 16)
	}
	branch := backendBranchFromOptions(state, opts)
	ensureBackendIDs(&state, &branch, opts)
	branch.Status = "pending"
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if tcpReady, _ := recordedComputeReady(branch); tcpReady {
		if ready, message, err := branchPostgresReady(ctx, branch); err != nil {
			return BranchActionResult{}, err
		} else if ready {
			branch.Status = "ready"
			state.Branches[opts.BranchID] = branch
			if err := WriteBackendState(path, state); err != nil {
				return BranchActionResult{}, err
			}
			return BranchActionResult{
				Status:   "ready",
				Message:  message,
				Endpoint: endpointFromBackendBranch(branch),
			}, nil
		} else if message != "" {
			state.Branches[opts.BranchID] = branch
			if err := WriteBackendState(path, state); err != nil {
				return BranchActionResult{}, err
			}
			return BranchActionResult{
				Status:  "pending",
				Message: message,
			}, nil
		}
	}
	message := fmt.Sprintf("neon-selfhost-driver recorded pending backend state for %q; storage cell or branch compute is not ready yet", opts.Branch)
	pageserverReady := false
	if ok, bootstrapMessage, err := ensurePageserverBackend(ctx, root, &state, &branch); err != nil {
		return BranchActionResult{}, err
	} else if bootstrapMessage != "" {
		message = bootstrapMessage
		if ok {
			pageserverReady = true
			branch.Status = "starting"
		}
	}
	if pageserverReady {
		if ok, computeMessage, err := ensureBranchCompute(ctx, root, state.TenantID, branch); err != nil {
			return BranchActionResult{}, err
		} else if computeMessage != "" {
			message = computeMessage
			if ok {
				branch.Status = "ready"
			} else if branch.Status != "pending" {
				branch.Status = "starting"
			}
		}
	}
	state.Branches[opts.BranchID] = branch
	if err := WriteBackendState(path, state); err != nil {
		return BranchActionResult{}, err
	}
	if branch.Status == "ready" {
		return BranchActionResult{
			Status:   "ready",
			Message:  message,
			Endpoint: endpointFromBackendBranch(branch),
		}, nil
	}
	return BranchActionResult{
		Status:  "pending",
		Message: message,
	}, nil
}

func inspectBackendStatus(rootOverride string) (string, backendStatusSummary, error) {
	root, err := substrateRoot(rootOverride)
	if err != nil {
		return "", backendStatusSummary{}, err
	}
	state, ok, err := ReadBackendState(filepath.Join(root, "backend.json"))
	if err != nil {
		return root, backendStatusSummary{SchemaVersion: BackendSchemaVersion}, err
	}
	if !ok {
		return root, backendStatusSummary{
			SchemaVersion: BackendSchemaVersion,
			Present:       false,
			Message:       "backend.json is not installed yet",
		}, nil
	}
	computes := 0
	for _, branch := range state.Branches {
		if strings.TrimSpace(branch.ComputeContainer) != "" {
			computes++
		}
	}
	return root, backendStatusSummary{
		SchemaVersion: BackendSchemaVersion,
		Present:       true,
		TenantID:      state.TenantID,
		BranchCount:   len(state.Branches),
		ComputeCount:  computes,
	}, nil
}

func substrateRoot(rootOverride string) (string, error) {
	root := strings.TrimSpace(rootOverride)
	if root == "" {
		root = strings.TrimSpace(envpolicy.Get(DriverRootEnv))
	}
	if root != "" {
		return filepath.Clean(root), nil
	}
	home := strings.TrimSpace(envpolicy.Get("ONLAVA_AGENT_HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(userHome, ".onlava")
	}
	return filepath.Join(filepath.Clean(home), "agent", "substrates", "neon"), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func safeIdentifier(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "branch"
	}
	return out
}
