package vnext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const (
	Edition           = "2027"
	ManifestVersion   = "scenery.manifest.v1"
	DiagnosticCatalog = "scenery.diagnostics.2027.v1"
)

var KernelProfiles = []string{
	"scenery.compiler-core/v1",
	"scenery.go-implementation/v1",
	"scenery.http-codec/v1",
	"scenery.runtime-http/v1",
	"scenery.inspection-core/v1",
}

var SupportedProfiles = map[string]bool{
	"scenery.compiler-core/v1":     true,
	"scenery.go-implementation/v1": true,
	"scenery.http-codec/v1":        true,
	"scenery.runtime-http/v1":      true,
	"scenery.inspection-core/v1":   true,
	"scenery.legacy-bridge/v1":     true,
	"scenery.typescript-client/v1": true,
}

type Position struct {
	Line       int `json:"line"`
	Column     int `json:"column"`
	ByteOffset int `json:"byte_offset"`
}

type Range struct {
	SourceID string   `json:"source_id"`
	Start    Position `json:"start"`
	End      Position `json:"end"`
}

type Diagnostic struct {
	Code        string         `json:"code"`
	Severity    string         `json:"severity"`
	Message     string         `json:"message"`
	Address     string         `json:"address,omitempty"`
	Path        string         `json:"path,omitempty"`
	Range       *Range         `json:"range,omitempty"`
	Related     []Related      `json:"related,omitempty"`
	Suggestions []string       `json:"suggestions,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
}

type Related struct {
	Address string `json:"address,omitempty"`
	Path    string `json:"path,omitempty"`
}

type Origin struct {
	Kind     string `json:"kind"`
	SourceID string `json:"source_id,omitempty"`
	Frontend string `json:"frontend,omitempty"`
}

type Resource struct {
	Address   string         `json:"address"`
	Kind      string         `json:"kind"`
	Name      string         `json:"name"`
	Module    string         `json:"module"`
	Spec      map[string]any `json:"spec"`
	Origin    Origin         `json:"origin"`
	Migration *MigrationMeta `json:"migration,omitempty"`
}

type MigrationMeta struct {
	State           string `json:"state"`
	Active          string `json:"active"`
	NativeCandidate string `json:"native_candidate,omitempty"`
}

type ApplicationIdentity struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Manifest struct {
	APIVersion        string                  `json:"api_version"`
	Edition           string                  `json:"edition"`
	DiagnosticCatalog string                  `json:"diagnostic_catalog"`
	Application       ApplicationIdentity     `json:"application"`
	Profiles          []string                `json:"profiles"`
	ContractRevision  string                  `json:"contract_revision"`
	Resources         []Resource              `json:"resources"`
	SourceMap         map[string]SourceRecord `json:"source_map"`
	Diagnostics       []Diagnostic            `json:"diagnostics"`
}

type SourceRecord struct {
	URI string `json:"uri"`
}

type Result struct {
	Root              string       `json:"-"`
	Manifest          *Manifest    `json:"manifest,omitempty"`
	WorkspaceRevision string       `json:"workspace_revision"`
	Diagnostics       []Diagnostic `json:"diagnostics"`
	Sources           []*Source    `json:"-"`
	Migration         *Migration   `json:"migration,omitempty"`
}

func (r *Result) Valid() bool {
	if r == nil || r.Manifest == nil {
		return false
	}
	for _, d := range r.Diagnostics {
		if d.Severity == "error" {
			return false
		}
	}
	return true
}

func canonicalResources(resources []Resource) ([]byte, error) {
	copyResources := append([]Resource(nil), resources...)
	sort.Slice(copyResources, func(i, j int) bool { return copyResources[i].Address < copyResources[j].Address })
	return json.Marshal(copyResources)
}

func contractRevision(resources []Resource, profiles []string, appName string) (string, error) {
	projected := make([]Resource, 0, len(resources))
	for _, resource := range resources {
		resource.Origin = Origin{}
		resource.Migration = nil
		projected = append(projected, resource)
	}
	sort.Strings(profiles)
	value := struct {
		Edition     string     `json:"edition"`
		Application string     `json:"application"`
		Profiles    []string   `json:"profiles"`
		Resources   []Resource `json:"resources"`
	}{Edition: Edition, Application: appName, Profiles: profiles, Resources: projected}
	b, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("scenery.contract-revision.v1\x00"), b...))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func kindForBlock(blockType string) string {
	switch blockType {
	case "binding":
		return "scenery.binding/v1"
	default:
		return "scenery." + strings.ReplaceAll(blockType, "_", "-") + "/v1"
	}
}

func resourceAddress(module, blockType, name string) string {
	if module == "" {
		module = "app"
	}
	return filepath.ToSlash(fmt.Sprintf("%s/%s/%s", module, blockType, name))
}
