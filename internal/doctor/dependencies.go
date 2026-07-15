package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type toolSpec struct {
	ID              string
	Name            string
	Command         string
	VersionArgs     []string
	Required        bool
	Relevant        bool
	MissingMessage  string
	FoundMessage    string
	SuggestedAction string
}

// DependencyChecks probes the external tools that scenery itself or the
// discovered app's configured features require or recommend.
func DependencyChecks(ctx context.Context, deps ProbeDeps, features AppFeatures, appFound bool) []Check {
	specs := []toolSpec{
		{
			ID:              "tool.go",
			Name:            "Go toolchain",
			Command:         "go",
			VersionArgs:     []string{"version"},
			Required:        true,
			Relevant:        true,
			MissingMessage:  "go not found; scenery requires Go 1.26 or newer",
			SuggestedAction: "Install Go 1.26 or newer and ensure `go` is on PATH.",
		},
		{
			ID:              "tool.bun",
			Name:            "Bun",
			Command:         "bun",
			VersionArgs:     []string{"--version"},
			Relevant:        true,
			MissingMessage:  bunMissingMessage(features, appFound),
			SuggestedAction: "Install Bun when working on dashboard UI, managed frontends, benchmarks, or TypeScript workers.",
		},
		{
			ID:              "tool.atlas",
			Name:            "Atlas",
			Command:         "atlas",
			VersionArgs:     []string{"version"},
			Relevant:        appFound && features.AtlasRelevant,
			MissingMessage:  "atlas not found; configured SQLC schema refresh uses Atlas source files",
			SuggestedAction: "Install Atlas if you need `scenery generate sqlc` to refresh schema SQL from Atlas definitions.",
		},
		{
			ID:              "tool.sqlc",
			Name:            "SQLC",
			Command:         "sqlc",
			VersionArgs:     []string{"version"},
			Relevant:        appFound && features.SQLCConfigured,
			MissingMessage:  "sqlc not found; configured SQLC generation requires it",
			SuggestedAction: "Install sqlc if you need `scenery generate sqlc`.",
		},
		{
			ID:              "tool.psql",
			Name:            "psql",
			Command:         "psql",
			VersionArgs:     []string{"--version"},
			Relevant:        appFound && features.PostgresServices,
			MissingMessage:  "psql not found; postgres services need it for `scenery db shell`",
			SuggestedAction: "Install PostgreSQL client tools if you need postgres shell access.",
		},
		{
			ID:              "tool.git",
			Name:            "Git",
			Command:         "git",
			VersionArgs:     []string{"--version"},
			Relevant:        true,
			MissingMessage:  "git not found; useful for source checkouts and release/debug metadata",
			SuggestedAction: "Install Git for normal source-control workflows.",
		},
	}
	checks := make([]Check, 0, len(specs))
	for _, spec := range specs {
		if !spec.Relevant {
			continue
		}
		checks = append(checks, toolCheck(ctx, deps, spec))
	}
	return checks
}

func bunMissingMessage(features AppFeatures, appFound bool) string {
	var uses []string
	if features.FrontendConfigured {
		uses = append(uses, "managed frontends")
	}
	if features.TypeScriptTasks {
		uses = append(uses, "TypeScript code tasks")
	}
	if len(uses) == 0 {
		if appFound {
			return "bun not found; only needed for dashboard UI, benchmarks, TypeScript workers, or TypeScript code tasks"
		}
		return "bun not found; optional unless you work on dashboard UI, benchmarks, TypeScript workers, or TypeScript code tasks"
	}
	return "bun not found; this app may need it for " + strings.Join(uses, ", ")
}

func toolCheck(ctx context.Context, deps ProbeDeps, spec toolSpec) Check {
	path, err := deps.LookPath(spec.Command)
	if err != nil {
		status := StatusWarn
		severity := SeverityOptional
		if spec.Required {
			status = StatusError
			severity = SeverityRequired
		}
		return Check{
			ID:              spec.ID,
			Category:        "dependency",
			Name:            spec.Name,
			Status:          status,
			Severity:        severity,
			Message:         spec.MissingMessage,
			SuggestedAction: spec.SuggestedAction,
			Observed:        map[string]any{"command": spec.Command},
		}
	}
	check := Check{
		ID:       spec.ID,
		Category: "dependency",
		Name:     spec.Name,
		Status:   StatusOK,
		Severity: SeverityOptional,
		Message:  spec.Name + " found at " + path,
		Observed: map[string]any{
			"command": spec.Command,
			"path":    path,
		},
	}
	if spec.Required {
		check.Severity = SeverityRequired
	}
	if len(spec.VersionArgs) > 0 {
		cmdCtx, cancel := context.WithTimeout(ctx, commandTimeout)
		out, err := deps.RunCommand(cmdCtx, path, spec.VersionArgs...)
		cancel()
		version := strings.TrimSpace(string(out))
		if version != "" {
			check.Observed["version"] = version
		}
		if err != nil {
			check.Status = StatusWarn
			if spec.Required {
				check.Status = StatusError
			}
			check.Message = spec.Name + " was found, but version probing failed"
			check.SuggestedAction = "Run `" + spec.Command + " " + strings.Join(spec.VersionArgs, " ") + "` manually and fix the command if it fails."
			return check
		}
		if version != "" {
			check.Message = version + " at " + path
		}
		if spec.ID == "tool.go" {
			versionInfo, ok := parseGoToolchainVersion(version)
			if !ok {
				check.Status = StatusError
				check.Message = "could not parse Go version output: " + version
				check.SuggestedAction = "Install Go 1.26 or newer and ensure `go version` reports a standard version."
			} else {
				check.Observed["parsed_version"] = versionInfo.String()
				if versionInfo.compare(goVersion{Major: minGoMajor, Minor: minGoMinor}) < 0 {
					check.Status = StatusError
					check.Message = fmt.Sprintf("%s found at %s; scenery requires Go %d.%d or newer", versionInfo.String(), path, minGoMajor, minGoMinor)
					check.SuggestedAction = "Install Go 1.26 or newer and ensure it appears first on PATH."
				}
			}
		}
	}
	return check
}

// DockerChecks reports Docker CLI presence, the selected Docker context,
// and Docker engine reachability.
func DockerChecks(ctx context.Context, deps ProbeDeps) []Check {
	path, err := deps.LookPath("docker")
	if err != nil {
		return []Check{{
			ID:              "docker.engine",
			Category:        "dependency",
			Name:            "Docker engine",
			Status:          StatusWarn,
			Severity:        SeverityOptional,
			Message:         "Docker CLI was not found; Docker engine cannot be probed",
			SuggestedAction: "Install Docker or configure non-Docker dev services when image-backed local services are needed.",
			Observed:        map[string]any{"command": "docker"},
		}}
	}
	return []Check{
		dockerContextCheck(ctx, deps, path),
		dockerEngineCheck(ctx, deps, path),
	}
}

func dockerContextCheck(ctx context.Context, deps ProbeDeps, path string) Check {
	check := Check{
		ID:       "docker.context",
		Category: "dependency",
		Name:     "Docker context",
		Status:   StatusOK,
		Severity: SeverityInformational,
		Message:  "Docker context is selected",
		Observed: map[string]any{"command": "docker", "path": path},
	}
	cmdCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	out, err := deps.RunCommand(cmdCtx, path, "context", "show")
	cancel()
	contextName := strings.TrimSpace(string(out))
	if contextName != "" {
		check.Observed["context"] = contextName
	}
	if err != nil {
		check.Status = StatusWarn
		check.Severity = SeverityOptional
		check.Message = "Docker CLI was found, but the current Docker context could not be determined"
		check.SuggestedAction = "Run `docker context show` manually and fix Docker context configuration if it fails."
		if contextName != "" {
			check.Observed["error_output"] = contextName
		}
		return check
	}
	if contextName != "" {
		check.Message = "Docker context " + contextName + " is selected"
	}
	return check
}

func dockerEngineCheck(ctx context.Context, deps ProbeDeps, path string) Check {
	check := Check{
		ID:       "docker.engine",
		Category: "dependency",
		Name:     "Docker engine",
		Status:   StatusOK,
		Severity: SeverityOptional,
		Message:  "Docker engine is reachable",
		Observed: map[string]any{"command": "docker", "path": path},
	}
	cmdCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	infoOut, infoErr := deps.RunCommand(cmdCtx, path, "info", "--format", "{{json .}}")
	cancel()
	if infoErr != nil {
		output := strings.TrimSpace(string(infoOut))
		check.Status = StatusWarn
		check.Message = "Docker CLI was found, but the Docker engine is not reachable"
		check.SuggestedAction = "Start Docker Desktop or the Docker daemon, then rerun `scenery doctor -o json`."
		if output != "" {
			check.Observed["error_output"] = output
		}
		return check
	}
	info := map[string]any{}
	if err := json.Unmarshal(bytes.TrimSpace(infoOut), &info); err != nil {
		output := strings.TrimSpace(string(infoOut))
		check.Status = StatusWarn
		check.Message = "Docker engine responded, but engine details could not be parsed"
		check.SuggestedAction = "Run `docker info --format '{{json .}}'` manually and check the output."
		if output != "" {
			check.Observed["raw_output"] = output
		}
		return check
	}
	for source, target := range map[string]string{
		"ServerVersion":   "server_version",
		"OperatingSystem": "operating_system",
		"OSType":          "os_type",
		"Architecture":    "architecture",
		"NCPU":            "cpus",
		"MemTotal":        "memory_bytes",
		"DockerRootDir":   "docker_root_dir",
		"Driver":          "storage_driver",
		"CgroupVersion":   "cgroup_version",
		"KernelVersion":   "kernel_version",
		"Name":            "name",
	} {
		if value, ok := info[source]; ok && !emptyObservedValue(value) {
			check.Observed[target] = value
		}
	}
	if version, _ := check.Observed["server_version"].(string); version != "" {
		check.Message = "Docker Engine " + version + " is reachable"
	}
	return check
}

// PostgresServerCheck reports whether managed postgres dev services can
// start: skipped when none are configured, an error when Docker is
// missing or unreachable.
func PostgresServerCheck(ctx context.Context, deps ProbeDeps, features AppFeatures) Check {
	check := Check{
		ID:       "db.postgres_server",
		Category: "database",
		Name:     "Managed Postgres dev server",
		Status:   StatusSkipped,
		Severity: SeverityInformational,
		Message:  "no postgres dev.services are configured",
	}
	if !features.PostgresServices {
		return check
	}
	check.Status = StatusOK
	check.Severity = SeverityRequired
	check.Message = "Docker is reachable for managed postgres dev services"
	path, err := deps.LookPath("docker")
	if err != nil {
		check.Status = StatusError
		check.Message = "Docker CLI was not found; managed postgres dev services cannot start"
		check.SuggestedAction = "Install Docker or set each postgres service database_url_env to an external postgres URL."
		check.Observed = map[string]any{"command": "docker"}
		return check
	}
	check.Observed = map[string]any{"command": "docker", "path": path}
	cmdCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	out, infoErr := deps.RunCommand(cmdCtx, path, "info", "--format", "{{json .}}")
	cancel()
	if infoErr != nil {
		check.Status = StatusError
		check.Message = "Docker engine is not reachable; managed postgres dev services cannot start"
		check.SuggestedAction = "Start Docker Desktop or set each postgres service database_url_env to an external postgres URL."
		if output := strings.TrimSpace(string(out)); output != "" {
			check.Observed["error_output"] = output
		}
	}
	return check
}

func emptyObservedValue(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(v) == ""
	default:
		return false
	}
}

type goVersion struct {
	Major int
	Minor int
	Patch int
}

var goVersionRE = regexp.MustCompile(`go([0-9]+)\.([0-9]+)(?:\.([0-9]+))?`)

func parseGoToolchainVersion(output string) (goVersion, bool) {
	match := goVersionRE.FindStringSubmatch(output)
	if len(match) == 0 {
		return goVersion{}, false
	}
	major, err := strconv.Atoi(match[1])
	if err != nil {
		return goVersion{}, false
	}
	minor, err := strconv.Atoi(match[2])
	if err != nil {
		return goVersion{}, false
	}
	patch := 0
	if match[3] != "" {
		if patch, err = strconv.Atoi(match[3]); err != nil {
			return goVersion{}, false
		}
	}
	return goVersion{Major: major, Minor: minor, Patch: patch}, true
}

func (v goVersion) compare(other goVersion) int {
	switch {
	case v.Major != other.Major:
		return v.Major - other.Major
	case v.Minor != other.Minor:
		return v.Minor - other.Minor
	default:
		return v.Patch - other.Patch
	}
}

func (v goVersion) String() string {
	if v.Patch == 0 {
		return fmt.Sprintf("go%d.%d", v.Major, v.Minor)
	}
	return fmt.Sprintf("go%d.%d.%d", v.Major, v.Minor, v.Patch)
}
