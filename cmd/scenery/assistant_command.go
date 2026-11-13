package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	inspectdata "scenery.sh/internal/inspect"
)

const assistantStatusKind = "scenery.assistant.status"

type assistantPolicyRecord struct {
	Authentication string `json:"authentication"`
	Authorization  string `json:"authorization"`
	Pipeline       string `json:"pipeline"`
}

type assistantRevisionsRecord struct {
	ExpectedRuntimeRevision    string `json:"expected_runtime_revision"`
	ExpectedCapabilityRevision string `json:"expected_capability_revision"`
	ActualRuntimeRevision      string `json:"actual_runtime_revision"`
	ActualCapabilityRevision   string `json:"actual_capability_revision"`
}

type assistantImplementationRecord struct {
	Adapter            string `json:"adapter,omitempty"`
	Source             string `json:"source,omitempty"`
	Package            string `json:"package,omitempty"`
	PackageLock        string `json:"package_lock,omitempty"`
	NodeVersion        string `json:"node_version,omitempty"`
	RuntimePackage     string `json:"runtime_package,omitempty"`
	PackageLockDigest  string `json:"package_lock_digest,omitempty"`
	OverlayPath        string `json:"overlay_path,omitempty"`
	PrivateControlAddr string `json:"private_control_addr,omitempty"`
	PrivateMCPAddr     string `json:"private_mcp_addr,omitempty"`
	PID                int    `json:"pid,omitempty"`
}

type assistantInspectionRecord struct {
	Address       string                   `json:"address"`
	Name          string                   `json:"name"`
	Path          string                   `json:"path"`
	Access        string                   `json:"access"`
	SessionAccess string                   `json:"session_access"`
	Policy        assistantPolicyRecord    `json:"policy"`
	Revisions     assistantRevisionsRecord `json:"revisions"`
	Ready         bool                     `json:"ready"`
	Status        string                   `json:"status"`
	RestartCount  int                      `json:"restart_count"`
	LastFailure   string                   `json:"last_failure"`
	LogSource     string                   `json:"log_source"`
}

type assistantStatusResponse struct {
	cliPayloadIdentity
	App       inspectdata.AppRef        `json:"app"`
	Assistant assistantInspectionRecord `json:"assistant"`
}

func assistantCommand(args []string) error {
	if len(args) == 0 {
		return usageErrorf("missing assistant subcommand")
	}
	if err := flagBeforeWord(args, "the assistant subcommand"); err != nil {
		return err
	}
	switch args[0] {
	case "status":
		return runAssistantStatus(args[1:], os.Stdout)
	default:
		return usageErrorf("unknown assistant command %q", args[0])
	}
}

func runAssistantStatus(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageErrorf("missing assistant name")
	}
	if err := flagBeforeWord(args, "the assistant name"); err != nil {
		return err
	}
	name := strings.TrimSpace(args[0])
	if name == "" {
		return usageErrorf("assistant name must not be empty")
	}
	flags := newCLIFlagSet("assistant status")
	jsonOutput := false
	registerJSONOutput(flags, &jsonOutput)
	var appRoot string
	flags.StringVar(&appRoot, "app-root", "", "")
	positionals, err := parseCLIFlags(flags, args[1:])
	if err != nil {
		return err
	}
	if len(positionals) != 0 {
		return usageErrorf("unexpected argument %q", positionals[0])
	}
	if !jsonOutput {
		return usageErrorf("scenery assistant status currently requires -o json")
	}
	root, err := resolveAppRoot(appRoot)
	if err != nil {
		return err
	}
	root, cfg, err := appcfg.DiscoverRoot(root)
	if err != nil {
		return err
	}
	compiled, err := compiler.Compile(root)
	if err != nil {
		return err
	}
	if !compiled.Valid() {
		return writeInspectCompileFailure(stdout, compiled)
	}
	resource, ok := assistantResourceByName(compiled.Manifest, name)
	if !ok {
		return fmt.Errorf("assistant %q not found", name)
	}
	records, err := readAssistantStatusSnapshot(root)
	if err != nil {
		return err
	}
	record := assistantInspectionFromResource(resource, compiled, records[resource.Address])
	return writeInspectJSON(stdout, assistantStatusResponse{cliPayloadIdentity: newCLIPayloadIdentity(assistantStatusKind), App: inspectAppInfo(root, cfg, nil), Assistant: record})
}

func assistantResourceByName(manifest *graph.Manifest, name string) (graph.Resource, bool) {
	if manifest == nil {
		return graph.Resource{}, false
	}
	name = strings.TrimSpace(name)
	for _, resource := range manifest.Resources {
		if resource.Kind != "scenery.assistant" {
			continue
		}
		if resource.Name == name || resource.Address == name {
			return resource, true
		}
	}
	return graph.Resource{}, false
}

func assistantInspectionFromResource(resource graph.Resource, compiled *compiler.Result, status assistantStatusRecord) assistantInspectionRecord {
	surface, _ := resource.Spec["surface"].(map[string]any)
	path := assistantStringValue(surface["path"])
	if path == "" {
		path = "/assistants/" + resource.Name
	}
	authentication := assistantReference(surface["authentication"])
	authorization := assistantReference(surface["authorization"])
	pipeline := assistantReference(surface["pipeline"])
	sessionAccess := assistantStringValue(surface["session_access"])
	if sessionAccess == "" {
		sessionAccess = "initiator"
	}
	access := "auth"
	expectedRuntime := assistantExpectedRuntimeRevision(compiled)
	expectedCapability := assistantCapabilityRevision(compiled, resource.Address)
	if status.Address == "" {
		status = assistantStatusRecord{
			Address: resource.Address, Name: resource.Name, Path: path, Access: access, SessionAccess: sessionAccess,
			Policy:                  assistantPolicyRecord{Authentication: authentication, Authorization: authorization, Pipeline: pipeline},
			ExpectedRuntimeRevision: expectedRuntime, ExpectedCapabilityRevision: expectedCapability,
			Ready: false, Status: "not_started", RestartCount: 0, LogSource: "assistant:" + resource.Name,
		}
	}
	record := assistantInspectionRecord{
		Address: resource.Address, Name: resource.Name, Path: path, SessionAccess: sessionAccess,
		Access: access,
		Policy: assistantPolicyRecord{Authentication: authentication, Authorization: authorization, Pipeline: pipeline},
		Revisions: assistantRevisionsRecord{
			ExpectedRuntimeRevision: expectedRuntime, ExpectedCapabilityRevision: expectedCapability,
			ActualRuntimeRevision: status.ActualRuntimeRevision, ActualCapabilityRevision: status.ActualCapabilityRevision,
		},
		Ready: status.Ready, Status: firstNonEmpty(status.Status, "not_started"), RestartCount: status.RestartCount,
		LastFailure: status.LastFailure, LogSource: firstNonEmpty(status.LogSource, "assistant:"+resource.Name),
	}
	return record
}

// writeAssistantLiveStatusSnapshot adapts the supervisor's provider-neutral
// live records to the canonical graph-backed snapshot consumed by status.
func writeAssistantLiveStatusSnapshot(appRoot string, compiled *compiler.Result, live []AssistantStatusRecord) error {
	if compiled == nil || compiled.Manifest == nil {
		return fmt.Errorf("assistant status snapshot requires a compiled manifest")
	}
	byAddress := make(map[string]graph.Resource)
	for _, resource := range compiled.Manifest.Resources {
		if resource.Kind == "scenery.assistant" {
			byAddress[resource.Address] = resource
		}
	}
	records := make([]assistantStatusRecord, 0, len(live))
	for _, current := range live {
		resource, ok := byAddress[current.Address]
		if !ok {
			continue
		}
		canonical := assistantInspectionFromResource(resource, compiled, assistantStatusRecord{})
		lastFailure := strings.TrimSpace(current.LastFailure)
		if lastFailure != "" && !assistantStatusCode(lastFailure) {
			lastFailure = "runtime_unavailable"
		}
		implementationSpec, _ := resource.Spec["implementation"].(map[string]any)
		record := assistantStatusRecord{
			Address: resource.Address, Name: canonical.Name, Path: canonical.Path, Access: canonical.Access,
			SessionAccess: canonical.SessionAccess, Policy: canonical.Policy,
			ExpectedRuntimeRevision:    canonical.Revisions.ExpectedRuntimeRevision,
			ExpectedCapabilityRevision: canonical.Revisions.ExpectedCapabilityRevision,
			ActualRuntimeRevision:      current.ActualRuntimeRevision, ActualCapabilityRevision: current.ActualCapabilityRevision,
			Ready: current.Ready, Status: firstNonEmpty(current.State, "not_started"), RestartCount: current.RestartCount,
			LastFailure: lastFailure, LogSource: firstNonEmpty(current.LogSource, "assistant:"+canonical.Name),
			Implementation: &assistantImplementationRecord{
				Adapter: assistantStringValue(implementationSpec["adapter"]), Source: assistantStringValue(implementationSpec["source"]),
				Package: assistantStringValue(implementationSpec["package"]), PackageLock: assistantStringValue(implementationSpec["package_lock"]),
				OverlayPath: current.OverlayPath, PrivateControlAddr: current.ControlAddress, PrivateMCPAddr: current.MCPAddress, PID: current.PID,
			},
		}
		records = append(records, record)
	}
	return writeAssistantStatusSnapshot(appRoot, records)
}

func assistantExpectedRuntimeRevision(result *compiler.Result) string {
	if result != nil {
		keys := make([]string, 0, len(result.ImplementationRevisions))
		for key := range result.ImplementationRevisions {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if value := strings.TrimSpace(result.ImplementationRevisions[key]); value != "" {
				return value
			}
		}
	}
	return "runtime-1"
}

func assistantReference(value any) string {
	if raw, ok := value.(string); ok {
		return strings.TrimSpace(raw)
	}
	if object, ok := value.(map[string]any); ok {
		return strings.TrimSpace(assistantStringValue(object["$ref"]))
	}
	return ""
}

func assistantStringValue(value any) string {
	if value == nil {
		return ""
	}
	if stringValue, ok := value.(string); ok {
		return strings.TrimSpace(stringValue)
	}
	return ""
}
