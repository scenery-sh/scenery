// Package doctor implements the scenery doctor check engine: host,
// resource, dependency, storage, and app readiness probes that produce
// stable check IDs, statuses, severities, and messages. CLI argument
// parsing and human/JSON rendering stay in cmd/scenery.
package doctor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/build"
)

// Check statuses and severities used in the scenery.doctor.result payload.
const (
	StatusOK              = "ok"
	StatusWarn            = "warn"
	StatusError           = "error"
	StatusSkipped         = "skipped"
	SeverityRequired      = "required"
	SeverityOptional      = "optional"
	SeverityInformational = "informational"
)

const (
	commandTimeout   = 2 * time.Second
	minGoMajor       = 1
	minGoMinor       = 26
	diskWarnBytes    = 5 * 1024 * 1024 * 1024
	diskErrorBytes   = 1 * 1024 * 1024 * 1024
	sizeWalkTimeout  = 2 * time.Second
	memoryWarnBytes  = 4 * 1024 * 1024 * 1024
	memoryErrorBytes = 2 * 1024 * 1024 * 1024
)

// Check is one doctor readiness check result.
type Check struct {
	ID              string         `json:"id"`
	Category        string         `json:"category"`
	Name            string         `json:"name"`
	Status          string         `json:"status"`
	Severity        string         `json:"severity"`
	Message         string         `json:"message"`
	SuggestedAction string         `json:"suggested_action,omitempty"`
	Observed        map[string]any `json:"observed,omitempty"`
}

// Summary counts doctor checks by status.
type Summary struct {
	OK       int `json:"ok"`
	Warnings int `json:"warnings"`
	Errors   int `json:"errors"`
	Skipped  int `json:"skipped"`
}

// AppInfo describes the discovered scenery app root.
type AppInfo struct {
	Root       string `json:"root"`
	ConfigPath string `json:"config_path"`
	Name       string `json:"name"`
	ID         string `json:"id,omitempty"`
}

// Environment reports the probed host runtime and inspected paths.
type Environment struct {
	GOOS             string       `json:"goos"`
	GOARCH           string       `json:"goarch"`
	NumCPU           int          `json:"num_cpu"`
	TotalMemoryBytes uint64       `json:"total_memory_bytes,omitempty"`
	Paths            []PathReport `json:"paths"`
}

// PathReport reports free and total disk space for one inspected path.
type PathReport struct {
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	FreeBytes  uint64 `json:"free_bytes,omitempty"`
	TotalBytes uint64 `json:"total_bytes,omitempty"`
}

// ProbeDeps is the dependency-injection seam for doctor checks. Zero
// fields are filled with production defaults by FillProbeDeps.
type ProbeDeps struct {
	LookPath      func(file string) (string, error)
	RunCommand    func(ctx context.Context, name string, args ...string) ([]byte, error)
	ResourceProbe ResourceProbe
	Getwd         func() (string, error)
	CacheRoot     func() (string, error)
	AgentHome     func() (string, error)
	DiscoverApp   func(start string) (AppInfo, appcfg.Config, bool, error)
}

// ResourceProbe reports host runtime, memory, and disk facts.
type ResourceProbe interface {
	Runtime() RuntimeInfo
	Memory(ctx context.Context) (MemoryInfo, error)
	Disk(ctx context.Context, path string) (DiskInfo, error)
}

// RuntimeInfo is the probed Go runtime platform.
type RuntimeInfo struct {
	GOOS   string
	GOARCH string
	NumCPU int
}

// MemoryInfo is the probed total physical memory.
type MemoryInfo struct {
	TotalBytes uint64
}

// DiskInfo is the probed disk usage for one path.
type DiskInfo struct {
	Path       string
	FreeBytes  uint64
	TotalBytes uint64
}

// DefaultProbeDeps returns the production probe dependencies.
func DefaultProbeDeps() ProbeDeps {
	return ProbeDeps{
		LookPath:      exec.LookPath,
		RunCommand:    runCommand,
		ResourceProbe: defaultResourceProbe{},
		Getwd:         os.Getwd,
		CacheRoot:     build.CacheRoot,
		AgentHome: func() (string, error) {
			paths, err := localagent.DefaultPaths()
			if err != nil {
				return "", err
			}
			return paths.Home, nil
		},
		DiscoverApp: func(start string) (AppInfo, appcfg.Config, bool, error) {
			root, cfg, err := appcfg.DiscoverRoot(start)
			if err != nil {
				return AppInfo{}, appcfg.Config{}, false, err
			}
			return AppInfo{
				Root:       root,
				ConfigPath: cfg.SourcePath(root),
				Name:       cfg.Name,
				ID:         cfg.ID,
			}, cfg, true, nil
		},
	}
}

// FillProbeDeps replaces every nil dependency with its production default.
func FillProbeDeps(deps ProbeDeps) ProbeDeps {
	defaults := DefaultProbeDeps()
	if deps.LookPath == nil {
		deps.LookPath = defaults.LookPath
	}
	if deps.RunCommand == nil {
		deps.RunCommand = defaults.RunCommand
	}
	if deps.ResourceProbe == nil {
		deps.ResourceProbe = defaults.ResourceProbe
	}
	if deps.Getwd == nil {
		deps.Getwd = defaults.Getwd
	}
	if deps.CacheRoot == nil {
		deps.CacheRoot = defaults.CacheRoot
	}
	if deps.AgentHome == nil {
		deps.AgentHome = defaults.AgentHome
	}
	if deps.DiscoverApp == nil {
		deps.DiscoverApp = defaults.DiscoverApp
	}
	return deps
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return bytes.TrimSpace(out.Bytes()), err
	}
	return bytes.TrimSpace(out.Bytes()), nil
}

// Summarize counts the given checks by status.
func Summarize(checks []Check) Summary {
	var summary Summary
	for _, check := range checks {
		switch check.Status {
		case StatusOK:
			summary.OK++
		case StatusWarn:
			summary.Warnings++
		case StatusError:
			summary.Errors++
		case StatusSkipped:
			summary.Skipped++
		}
	}
	return summary
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	value := float64(n)
	for i, suffix := range units {
		value /= unit
		if value < unit || i == len(units)-1 {
			if value >= 10 {
				return fmt.Sprintf("%.0f %s", value, suffix)
			}
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%d B", n)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type defaultResourceProbe struct{}

func (defaultResourceProbe) Runtime() RuntimeInfo {
	return RuntimeInfo{
		GOOS:   runtime.GOOS,
		GOARCH: runtime.GOARCH,
		NumCPU: runtime.NumCPU(),
	}
}
