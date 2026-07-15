package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/generate"
)

// RuntimeCheck reports whether the host OS/architecture is a routinely
// tested scenery development platform.
func RuntimeCheck(info RuntimeInfo) Check {
	status := StatusOK
	severity := SeverityInformational
	message := info.GOOS + "/" + info.GOARCH
	action := ""
	if !supportedRuntime(info.GOOS) {
		status = StatusWarn
		severity = SeverityOptional
		message += " is not a routinely tested scenery development platform"
		action = "Prefer linux, darwin, or windows for local development when possible."
	}
	return Check{
		ID:              "os.runtime",
		Category:        "host",
		Name:            "Operating system",
		Status:          status,
		Severity:        severity,
		Message:         message,
		SuggestedAction: action,
		Observed: map[string]any{
			"goos":   info.GOOS,
			"goarch": info.GOARCH,
		},
	}
}

func supportedRuntime(goos string) bool {
	switch goos {
	case "linux", "darwin", "windows":
		return true
	default:
		return false
	}
}

// CPUCheck reports whether the host has enough logical CPUs for local dev.
func CPUCheck(numCPU int) Check {
	check := Check{
		ID:       "resource.cpu",
		Category: "resource",
		Name:     "CPU",
		Status:   StatusOK,
		Severity: SeverityInformational,
		Message:  fmt.Sprintf("%d logical CPUs", numCPU),
		Observed: map[string]any{"num_cpu": numCPU},
	}
	if numCPU < 2 {
		check.Status = StatusWarn
		check.Severity = SeverityOptional
		check.Message = fmt.Sprintf("%d logical CPU; local dev may be slow", numCPU)
		check.SuggestedAction = "Use at least 2 logical CPUs for smoother scenery development."
	}
	return check
}

// MemoryCheck reports whether total physical memory meets the scenery
// development minimums.
func MemoryCheck(memory MemoryInfo) Check {
	check := Check{
		ID:       "resource.memory",
		Category: "resource",
		Name:     "System memory",
		Status:   StatusOK,
		Severity: SeverityInformational,
		Message:  fmt.Sprintf("%s total memory", humanBytes(memory.TotalBytes)),
		Observed: map[string]any{"total_bytes": memory.TotalBytes},
	}
	switch {
	case memory.TotalBytes < memoryErrorBytes:
		check.Status = StatusError
		check.Severity = SeverityRequired
		check.Message = fmt.Sprintf("%s total memory; below the %s minimum", humanBytes(memory.TotalBytes), humanBytes(memoryErrorBytes))
		check.SuggestedAction = "Use a machine or container with more memory for scenery development."
	case memory.TotalBytes < memoryWarnBytes:
		check.Status = StatusWarn
		check.Severity = SeverityOptional
		check.Message = fmt.Sprintf("%s total memory; local dev may be slow", humanBytes(memory.TotalBytes))
		check.SuggestedAction = "Use at least 4 GiB RAM for smoother scenery development."
	}
	return check
}

// DiskPaths returns the deduplicated list of paths whose disk space
// doctor should inspect: the app root (or requested app root, or the
// current directory) plus the build cache root.
func DiskPaths(appRoot string, app *AppInfo, deps ProbeDeps) []PathReport {
	seen := map[string]bool{}
	var out []PathReport
	add := func(kind, path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err == nil {
			path = abs
		}
		key := kind + "\x00" + path
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, PathReport{Kind: kind, Path: path})
	}
	if app != nil {
		add("app_root", app.Root)
	} else if strings.TrimSpace(appRoot) != "" {
		add("app_root", appRoot)
	} else if cwd, err := deps.Getwd(); err == nil {
		add("cwd", cwd)
	}
	if cacheRoot, err := deps.CacheRoot(); err == nil {
		add("cache_root", cacheRoot)
	}
	return out
}

// DiskCheck probes free disk space for one path, appends the probed
// report to env.Paths on success, and returns the resulting checks.
func DiskCheck(ctx context.Context, probe ResourceProbe, path PathReport, env *Environment) []Check {
	disk, err := probe.Disk(ctx, path.Path)
	if err != nil {
		return []Check{{
			ID:              "resource.disk." + path.Kind,
			Category:        "resource",
			Name:            "Disk space (" + path.Kind + ")",
			Status:          StatusSkipped,
			Severity:        SeverityInformational,
			Message:         "disk space could not be determined for " + path.Path + ": " + err.Error(),
			SuggestedAction: "Verify free disk space manually if local builds or caches fail.",
			Observed:        map[string]any{"path": path.Path, "kind": path.Kind},
		}}
	}
	report := PathReport{
		Kind:       path.Kind,
		Path:       firstNonEmpty(disk.Path, path.Path),
		FreeBytes:  disk.FreeBytes,
		TotalBytes: disk.TotalBytes,
	}
	env.Paths = append(env.Paths, report)
	check := Check{
		ID:       "resource.disk." + path.Kind,
		Category: "resource",
		Name:     "Disk space (" + path.Kind + ")",
		Status:   StatusOK,
		Severity: SeverityInformational,
		Message:  fmt.Sprintf("%s free at %s", humanBytes(disk.FreeBytes), report.Path),
		Observed: map[string]any{
			"path":        report.Path,
			"kind":        path.Kind,
			"free_bytes":  disk.FreeBytes,
			"total_bytes": disk.TotalBytes,
		},
	}
	switch {
	case disk.FreeBytes < diskErrorBytes:
		check.Status = StatusError
		check.Severity = SeverityRequired
		check.Message = fmt.Sprintf("%s free at %s; below the %s minimum", humanBytes(disk.FreeBytes), report.Path, humanBytes(diskErrorBytes))
		check.SuggestedAction = "Free disk space before running builds, dev services, or managed tool downloads."
	case disk.FreeBytes < diskWarnBytes:
		check.Status = StatusWarn
		check.Severity = SeverityOptional
		check.Message = fmt.Sprintf("%s free at %s; local builds and caches may run out of space", humanBytes(disk.FreeBytes), report.Path)
		check.SuggestedAction = "Keep at least 5 GiB free for scenery build workspaces, caches, and local dev state."
	}
	return []Check{check}
}

// StorageSizeChecks reports the on-disk size of the Scenery home and its
// managed Postgres state.
func StorageSizeChecks(ctx context.Context, deps ProbeDeps) []Check {
	home, err := deps.AgentHome()
	if err != nil {
		return []Check{{
			ID:              "storage.scenery_home",
			Category:        "storage",
			Name:            "Scenery home size",
			Status:          StatusSkipped,
			Severity:        SeverityInformational,
			Message:         "Scenery home size could not be determined: " + err.Error(),
			SuggestedAction: "Verify `SCENERY_AGENT_HOME` or the current user's home directory if local state inspection fails.",
		}}
	}
	home = filepath.Clean(home)
	return []Check{
		pathSizeCheck(ctx, "storage.scenery_home", "Scenery home size", home, "Scenery home"),
		pathSizeCheck(ctx, "storage.postgres_database", "Postgres database state size", filepath.Join(home, "agent", "postgres"), "Postgres database state"),
	}
}

type pathSizeInfo struct {
	Path      string
	SizeBytes uint64
	FileCount int
	DirCount  int
}

func pathSizeCheck(ctx context.Context, id, name, path, label string) Check {
	check := Check{
		ID:       id,
		Category: "storage",
		Name:     name,
		Status:   StatusOK,
		Severity: SeverityInformational,
		Observed: map[string]any{"path": path},
	}
	sizeCtx, cancel := context.WithTimeout(ctx, sizeWalkTimeout)
	usage, err := pathSize(sizeCtx, path)
	cancel()
	if errors.Is(err, os.ErrNotExist) {
		check.Status = StatusSkipped
		check.Message = label + " is not present at " + path
		return check
	}
	if err != nil {
		check.Status = StatusSkipped
		check.Message = label + " size could not be determined for " + path + ": " + err.Error()
		check.SuggestedAction = "Inspect the path manually if local state appears unexpectedly large."
		return check
	}
	check.Message = fmt.Sprintf("%s at %s", humanBytes(usage.SizeBytes), usage.Path)
	check.Observed["path"] = usage.Path
	check.Observed["size_bytes"] = usage.SizeBytes
	check.Observed["file_count"] = usage.FileCount
	check.Observed["dir_count"] = usage.DirCount
	return check
}

func pathSize(ctx context.Context, path string) (pathSizeInfo, error) {
	path = filepath.Clean(path)
	if err := ctx.Err(); err != nil {
		return pathSizeInfo{}, err
	}
	rootInfo, err := os.Lstat(path)
	if err != nil {
		return pathSizeInfo{}, err
	}
	usage := pathSizeInfo{Path: path}
	if !rootInfo.IsDir() {
		if rootInfo.Size() > 0 {
			usage.SizeBytes = uint64(rootInfo.Size())
		}
		usage.FileCount = 1
		return usage, nil
	}
	err = filepath.WalkDir(path, func(_ string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			usage.DirCount++
		} else {
			usage.FileCount++
			if info.Size() > 0 {
				usage.SizeBytes += uint64(info.Size())
			}
		}
		return nil
	})
	return usage, err
}

// EditorWorkspaceCheck reports whether the app root's generated Go
// editor workspace is present, current, and unconflicted.
func EditorWorkspaceCheck(root string) Check {
	check := Check{
		ID:       "app.editor_workspace",
		Category: "app",
		Name:     "Generated Go editor workspace",
		Status:   StatusSkipped,
		Severity: SeverityInformational,
		Message:  "editor contracts have not been synchronized; run `scenery check`",
	}
	status := generate.InspectEditorWorkspace(root)
	check.Observed = map[string]any{"go_work": status.WorkFile, "owner": status.OwnerFile}
	if status.ParentWorkFile != "" {
		check.Observed["parent_go_work"] = status.ParentWorkFile
	}
	if status.Conflict {
		check.Status = StatusError
		check.Severity = SeverityRequired
		check.Message = status.Message
		check.SuggestedAction = "Remove or restore the conflicting root go.work, then run `scenery check`; Scenery never replaces an unverified workfile."
		return check
	}
	if !status.Managed {
		return check
	}
	check.Status = StatusOK
	check.Message = "generated Go contracts are available to raw Go commands and gopls"
	check.Observed["spec_revision"] = status.SpecRevision
	check.Observed["contract_revision"] = status.ContractRevision
	if compiled, err := compiler.Compile(root); err == nil && compiled.Manifest != nil && compiled.Manifest.ContractRevision != status.ContractRevision {
		check.Status = StatusWarn
		check.Severity = SeverityOptional
		check.Message = "editor contracts correspond to the previous valid contract revision"
		check.SuggestedAction = "Fix contract diagnostics, then run `scenery check` to refresh editor contracts."
		check.Observed["current_contract_revision"] = compiled.Manifest.ContractRevision
	}
	if status.ParentWorkFile != "" && check.Status == StatusOK {
		check.Status = StatusWarn
		check.Severity = SeverityOptional
		check.Message = "the managed app go.work shadows a parent Go workspace"
		check.SuggestedAction = "Run Go commands from the app root; remove the managed workfile only if the parent workspace must control this app."
	}
	return check
}
