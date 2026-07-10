package vnext

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Migration struct {
	Frontend     string             `json:"frontend"`
	LegacyConfig string             `json:"legacy_config,omitempty"`
	Services     []MigrationService `json:"services"`
	Source       *Source            `json:"-"`
}

type MigrationService struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	Active       string `json:"active"`
	Package      string `json:"package,omitempty"`
	Module       string `json:"module,omitempty"`
	LegacyTarget string `json:"legacy_target,omitempty"`
}

func parseMigration(root string) (*Migration, []Diagnostic) {
	path := filepath.Join(root, "scenery.migration.scn")
	_, statErr := os.Stat(path)
	hasLegacyConfig := pathExists(filepath.Join(root, ".scenery.json")) || pathExists(filepath.Join(root, ".config.json"))
	if os.IsNotExist(statErr) {
		if hasLegacyConfig {
			return nil, []Diagnostic{{Code: "SCN5001", Severity: "error", Message: "scenery.scn and a legacy app config require scenery.migration.scn"}}
		}
		return nil, nil
	}
	if statErr != nil {
		return nil, []Diagnostic{{Code: "SCN5002", Severity: "error", Message: statErr.Error()}}
	}
	source, diagnostics := parseSource(root, path)
	if source == nil || len(source.Blocks) == 0 {
		return nil, diagnostics
	}
	if len(source.Blocks) != 1 || source.Blocks[0].Type != "migration" {
		return nil, append(diagnostics, Diagnostic{Code: "SCN5003", Severity: "error", Message: "scenery.migration.scn requires exactly one migration block"})
	}
	block := source.Blocks[0]
	migration := &Migration{Source: source}
	migration.Frontend, _ = literalString(block, "frontend")
	migration.LegacyConfig, _ = literalString(block, "legacy_config")
	if migration.Frontend != "scenery.legacy.v0" {
		diagnostics = append(diagnostics, diagnosticForBlock("SCN5004", "migration frontend must be \"scenery.legacy.v0\"", block))
	}
	for _, child := range block.Blocks {
		if child.Type != "legacy_service" && child.Type != "shadow_service" && child.Type != "native_service" {
			diagnostics = append(diagnostics, diagnosticForBlock("SCN5005", "unknown migration block "+child.Type, child))
			continue
		}
		if len(child.Labels) != 1 {
			diagnostics = append(diagnostics, diagnosticForBlock("SCN5006", child.Type+" requires one service label", child))
			continue
		}
		service := MigrationService{Name: child.Labels[0]}
		switch child.Type {
		case "legacy_service":
			service.State, service.Active = "legacy", "legacy"
		case "shadow_service":
			service.State = "shadow"
			service.Active, _ = literalString(child, "active")
			if service.Active != "legacy" && service.Active != "native" {
				diagnostics = append(diagnostics, diagnosticForBlock("SCN5007", "shadow_service active must be legacy or native", child))
			}
		case "native_service":
			service.State, service.Active = "native", "native"
		}
		service.Package, _ = literalString(child, "package")
		if expression, ok := child.Attributes["module"]; ok {
			service.Module = expression.Traversal
		}
		if expression, ok := child.Attributes["target"]; ok {
			service.LegacyTarget = expression.Traversal
		}
		if expression, ok := child.Attributes["legacy_target"]; ok {
			service.LegacyTarget = expression.Traversal
		}
		migration.Services = append(migration.Services, service)
	}
	sort.Slice(migration.Services, func(i, j int) bool { return migration.Services[i].Name < migration.Services[j].Name })
	return migration, diagnostics
}

func (m *Migration) validate(resources []Resource) []Diagnostic {
	if m == nil {
		return nil
	}
	var diagnostics []Diagnostic
	seen := map[string]bool{}
	modules := map[string]bool{}
	for _, resource := range resources {
		if resource.Kind == "scenery.module/v1" {
			modules[resource.Name] = true
		}
	}
	for _, service := range m.Services {
		if seen[service.Name] {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5101", Severity: "error", Message: "duplicate migration service " + service.Name})
			continue
		}
		seen[service.Name] = true
		if service.State != "legacy" && !modules[service.Name] && strings.TrimPrefix(service.Module, "module.") != service.Name {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5102", Severity: "error", Message: "native migration service " + service.Name + " has no installed module"})
		}
		if service.State != "native" && strings.TrimSpace(service.Package) == "" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5103", Severity: "error", Message: "legacy migration service " + service.Name + " requires package"})
		}
		if service.State != "native" && strings.TrimSpace(service.LegacyTarget) == "" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5104", Severity: "error", Message: "legacy migration service " + service.Name + " requires an explicit Go target"})
		}
	}
	return diagnostics
}

func applyMigration(resources []Resource, migration *Migration) {
	if migration == nil {
		return
	}
	states := map[string]MigrationService{}
	for _, service := range migration.Services {
		states[service.Name] = service
	}
	for i := range resources {
		resource := &resources[i]
		if resource.Module == "app" {
			continue
		}
		service, ok := states[resource.Module]
		if !ok {
			continue
		}
		resource.Migration = &MigrationMeta{State: service.State, Active: service.Active}
		if service.State == "shadow" {
			resource.Migration.NativeCandidate = resource.Address
		}
	}
}

func pathExists(path string) bool { _, err := os.Stat(path); return err == nil }

type MigrationStatus struct {
	APIVersion        string             `json:"api_version"`
	Mode              string             `json:"mode"`
	Frontend          string             `json:"frontend,omitempty"`
	LegacyConfig      string             `json:"legacy_config,omitempty"`
	Ready             bool               `json:"ready"`
	Services          []MigrationService `json:"services"`
	WorkspaceRevision string             `json:"workspace_revision"`
	ContractRevision  string             `json:"contract_revision,omitempty"`
	Diagnostics       []Diagnostic       `json:"diagnostics"`
}

func BuildMigrationStatus(result *Result) MigrationStatus {
	status := MigrationStatus{APIVersion: "scenery.migrate.status.v1", Mode: "native_only", Ready: result != nil && result.Valid(), Diagnostics: []Diagnostic{}}
	if result == nil {
		status.Ready = false
		status.Diagnostics = []Diagnostic{{Code: "SCN9003", Severity: "error", Message: "missing compilation result"}}
		return status
	}
	status.WorkspaceRevision = result.WorkspaceRevision
	status.Diagnostics = append(status.Diagnostics, result.Diagnostics...)
	if result.Manifest != nil {
		status.ContractRevision = result.Manifest.ContractRevision
	}
	if result.Migration != nil {
		status.Mode = "mixed"
		status.Frontend = result.Migration.Frontend
		status.LegacyConfig = result.Migration.LegacyConfig
		status.Services = append(status.Services, result.Migration.Services...)
	}
	return status
}

func (s MigrationStatus) Service(name string) (MigrationService, error) {
	for _, service := range s.Services {
		if service.Name == name {
			return service, nil
		}
	}
	return MigrationService{}, fmt.Errorf("migration service %q not found", name)
}
