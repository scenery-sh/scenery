package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/assistantadapter/eve"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/evolution"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/scn"
)

const (
	assistantInitKind   = "scenery.assistant.init"
	assistantSourceRoot = "assistants"
)

type assistantInitFile struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}

type assistantInitResponse struct {
	cliPayloadIdentity
	Assistant                  string              `json:"assistant"`
	Address                    string              `json:"address"`
	MCPServer                  string              `json:"mcp_server"`
	Client                     string              `json:"client"`
	Source                     string              `json:"source"`
	Package                    string              `json:"package"`
	PackageLock                string              `json:"package_lock"`
	EvalDirectory              string              `json:"eval_directory"`
	DryRun                     bool                `json:"dry_run"`
	Applied                    bool                `json:"applied"`
	Idempotent                 bool                `json:"idempotent"`
	Created                    []string            `json:"created"`
	Preserved                  []string            `json:"preserved"`
	PlanID                     string              `json:"plan_id,omitempty"`
	BaseWorkspaceRevision      string              `json:"base_workspace_revision"`
	PredictedWorkspaceRevision string              `json:"predicted_workspace_revision"`
	ContractRevision           string              `json:"contract_revision"`
	Files                      []assistantInitFile `json:"files"`
}

type assistantScaffoldOptions struct {
	Name      string
	MCPServer string
	Client    string
	AppRoot   string
	JSON      bool
	DryRun    bool
}

type assistantInitDependencies struct {
	prepareChangeRequest func(evolution.ChangeRequest) evolution.ChangeRequest
}

func runAssistantInit(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("missing assistant name")
	}
	opts := assistantScaffoldOptions{Name: strings.TrimSpace(args[0])}
	if !validAssistantName(opts.Name) {
		return fmt.Errorf("assistant name %q must be lower_snake_case", opts.Name)
	}
	flags := newCLIFlagSet("assistant init")
	flags.StringVar(&opts.MCPServer, "mcp-server", "", "")
	flags.StringVar(&opts.Client, "client", "", "")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.BoolVar(&opts.DryRun, "dry-run", false, "")
	registerJSONOutput(flags, &opts.JSON)
	positionals, err := parseCLIFlags(flags, args[1:])
	if err != nil {
		return err
	}
	if len(positionals) != 0 {
		return fmt.Errorf("unexpected argument %q", positionals[0])
	}
	if !opts.JSON {
		return errors.New("scenery assistant init currently requires -o json")
	}
	if strings.TrimSpace(opts.MCPServer) == "" || strings.TrimSpace(opts.Client) == "" {
		return errors.New("assistant init requires --mcp-server and --client")
	}
	root, cfg, compiled, err := loadAssistantApp(opts.AppRoot)
	if err != nil {
		return err
	}
	response, err := initializeAssistant(context.Background(), root, cfg, compiled, opts)
	if err != nil {
		return err
	}
	return writeCLIJSON(stdout, response)
}

func loadAssistantApp(appRoot string) (string, appcfg.Config, *compiler.Result, error) {
	root, err := resolveAppRoot(appRoot)
	if err != nil {
		return "", appcfg.Config{}, nil, err
	}
	root, cfg, err := appcfg.DiscoverRoot(root)
	if err != nil {
		return "", appcfg.Config{}, nil, err
	}
	compiled, err := compiler.Compile(root)
	if err != nil {
		return "", appcfg.Config{}, nil, err
	}
	if compiled == nil || !compiled.Valid() || compiled.Manifest == nil {
		if compiled != nil {
			return "", appcfg.Config{}, nil, writeInspectCompileFailure(io.Discard, compiled)
		}
		return "", appcfg.Config{}, nil, errors.New("assistant app contract is invalid")
	}
	return root, cfg, compiled, nil
}

func initializeAssistant(ctx context.Context, root string, cfg appcfg.Config, compiled *compiler.Result, opts assistantScaffoldOptions) (assistantInitResponse, error) {
	return initializeAssistantWithDependencies(ctx, root, cfg, compiled, opts, assistantInitDependencies{
		prepareChangeRequest: withPredictedGenerateChecks,
	})
}

func initializeAssistantWithDependencies(ctx context.Context, root string, cfg appcfg.Config, compiled *compiler.Result, opts assistantScaffoldOptions, deps assistantInitDependencies) (assistantInitResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	server, ok := assistantResourceByKindAndName(compiled.Manifest, opts.MCPServer, "scenery.mcp-server")
	if !ok || server.Kind != "scenery.mcp-server" {
		return assistantInitResponse{}, fmt.Errorf("mcp server %q not found", opts.MCPServer)
	}
	client, ok := assistantResourceByKindAndName(compiled.Manifest, opts.Client, "scenery.typescript-client")
	if !ok || client.Kind != "scenery.typescript-client" {
		return assistantInitResponse{}, fmt.Errorf("typescript client %q not found", opts.Client)
	}
	gateway, err := assistantClientGateway(client)
	if err != nil {
		return assistantInitResponse{}, err
	}
	name := opts.Name
	address := "app/assistant/" + name
	source := "./" + filepath.ToSlash(filepath.Join(assistantSourceRoot, name))
	packagePath := source + "/package.json"
	lockPath := source + "/package-lock.json"
	evalPath := source + "/eval"
	files, err := scaffoldFiles(root, source, evalPath)
	if err != nil {
		return assistantInitResponse{}, err
	}
	existing, assistantExists := assistantResourceByName(compiled.Manifest, name)
	if assistantExists && existing.Address != address {
		return assistantInitResponse{}, fmt.Errorf("assistant %q already exists at %s", name, existing.Address)
	}
	operations := []evolution.SemanticOperation(nil)
	if !assistantExists {
		operations = append(operations, evolution.SemanticOperation{
			Op: "resource.create", Address: address,
			Value: map[string]any{
				"mcp_server": map[string]any{"$ref": "mcp_server." + opts.MCPServer},
				"implementation": map[string]any{
					"adapter": "eve", "source": source, "package": packagePath, "package_lock": lockPath,
				},
				"surface": map[string]any{
					"gateway":        map[string]any{"$ref": gateway},
					"path":           "/assistants/" + name,
					"authentication": map[string]any{"$ref": "std.authentication.none"},
					"authorization":  map[string]any{"$ref": "std.authorization.public"},
					"pipeline":       map[string]any{"$ref": "std.pipeline.empty"},
					"session_access": "initiator",
					"client":         map[string]any{"$ref": "typescript_client." + opts.Client},
				},
			},
		})
	}
	response := assistantInitResponse{
		cliPayloadIdentity: newCLIPayloadIdentity(assistantInitKind), Assistant: name, Address: address,
		MCPServer: opts.MCPServer, Client: opts.Client, Source: source, Package: packagePath, PackageLock: lockPath,
		EvalDirectory: evalPath, DryRun: opts.DryRun, Applied: false, Idempotent: assistantExists,
		Created: []string{}, Preserved: []string{}, Files: files,
		BaseWorkspaceRevision: compiled.WorkspaceRevision,
		ContractRevision:      compiled.Manifest.ContractRevision,
	}
	for _, file := range files {
		if file.Action == "create" {
			response.Created = append(response.Created, file.Path)
		} else {
			response.Preserved = append(response.Preserved, file.Path)
		}
	}
	sort.Strings(response.Created)
	sort.Strings(response.Preserved)
	needsFiles := len(response.Created) != 0
	if !assistantExists || needsFiles {
		additionalEdits, err := scaffoldSourceEdits(root, source, files)
		if err != nil {
			return assistantInitResponse{}, err
		}
		request := evolution.ChangeRequest{
			BaseWorkspaceRevision: compiled.WorkspaceRevision,
			BaseContractRevision:  revisionFlag(compiled.Manifest.ContractRevision),
			Caller:                "scenery assistant init",
			Capabilities:          []string{"scenery.agent-mutation"},
			Operations:            operations,
			AdditionalEdits:       additionalEdits,
		}
		if deps.prepareChangeRequest != nil {
			request = deps.prepareChangeRequest(request)
		}
		var plan evolution.ChangePlan
		if opts.DryRun {
			plan, err = evolution.PlanChangesDryRun(root, request)
		} else {
			plan, err = evolution.PlanChanges(root, request)
		}
		if err != nil {
			return assistantInitResponse{}, fmt.Errorf("assistant init plan: %w", err)
		}
		response.PlanID = plan.PlanID
		response.PredictedWorkspaceRevision = plan.PredictedWorkspaceRevision
		if !opts.DryRun {
			if _, err := evolution.ApplyChangePlanWithOptions(root, plan, evolution.ApplyOptions{
				ExpectedWorkspaceRevision: compiled.WorkspaceRevision,
				ExpectedContractRevision:  revisionFlag(compiled.Manifest.ContractRevision),
				Caller:                    plan.Caller,
				GrantedCapabilities:       []string{"scenery.agent-mutation"},
				SkipGeneratedValidation:   true,
			}); err != nil {
				return assistantInitResponse{}, fmt.Errorf("assistant init apply: %w", err)
			}
			response.Applied = true
			response.Idempotent = false
		}
	} else {
		response.PredictedWorkspaceRevision = compiled.WorkspaceRevision
	}
	if !opts.DryRun {
		if err := ensureAssistantEvalDirectory(root, evalPath); err != nil {
			return assistantInitResponse{}, err
		}
	}
	return response, nil
}

func assistantClientGateway(client graph.Resource) (string, error) {
	gateways, ok := client.Spec["gateways"].([]any)
	if !ok || len(gateways) == 0 {
		return "", fmt.Errorf("typescript client %q has no HTTP gateway", client.Name)
	}
	for _, value := range gateways {
		if reference := assistantReference(value); reference != "" {
			if strings.HasPrefix(reference, "app/http_gateway/") {
				return reference, nil
			}
			if strings.HasPrefix(reference, "http_gateway.") {
				return reference, nil
			}
		}
	}
	return "", fmt.Errorf("typescript client %q has no usable HTTP gateway", client.Name)
}

func assistantResourceByKindAndName(manifest *graph.Manifest, name, kind string) (graph.Resource, bool) {
	if manifest == nil {
		return graph.Resource{}, false
	}
	name = strings.TrimSpace(name)
	for _, resource := range manifest.Resources {
		if resource.Kind == kind && (resource.Name == name || resource.Address == name) {
			return resource, true
		}
	}
	return graph.Resource{}, false
}

func scaffoldFiles(root, source, evalPath string) ([]assistantInitFile, error) {
	assets, err := eve.ScaffoldPackageFiles()
	if err != nil {
		return nil, fmt.Errorf("read assistant scaffold assets: %w", err)
	}
	_ = assets
	paths := []string{
		filepath.ToSlash(filepath.Join(source, "package.json")),
		filepath.ToSlash(filepath.Join(source, "package-lock.json")),
		filepath.ToSlash(filepath.Join(source, "agent", "agent.ts")),
		filepath.ToSlash(filepath.Join(source, "agent", "instructions.md")),
	}
	result := make([]assistantInitFile, 0, len(paths))
	for _, relative := range paths {
		absolute := filepath.Join(root, filepath.FromSlash(relative))
		info, statErr := os.Lstat(absolute)
		if statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return nil, fmt.Errorf("assistant scaffold target %q is not a regular file", relative)
			}
			result = append(result, assistantInitFile{Path: relative, Action: "preserve"})
			continue
		}
		if !os.IsNotExist(statErr) {
			return nil, statErr
		}
		result = append(result, assistantInitFile{Path: relative, Action: "create"})
	}
	if info, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(evalPath))); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("assistant eval path %q is not a regular directory", evalPath)
		}
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}
	return result, nil
}

func scaffoldSourceEdits(root, source string, files []assistantInitFile) ([]evolution.SourceEdit, error) {
	assets, err := eve.ScaffoldPackageFiles()
	if err != nil {
		return nil, err
	}
	contents := map[string][]byte{
		filepath.ToSlash(filepath.Join(source, "package.json")):             assets["package.json"],
		filepath.ToSlash(filepath.Join(source, "package-lock.json")):        assets["package-lock.json"],
		filepath.ToSlash(filepath.Join(source, "agent", "agent.ts")):        []byte(scaffoldAgentSource),
		filepath.ToSlash(filepath.Join(source, "agent", "instructions.md")): []byte(scaffoldInstructions),
	}
	result := make([]evolution.SourceEdit, 0, len(files))
	for _, file := range files {
		absolute := filepath.Join(root, filepath.FromSlash(file.Path))
		before, exists, mode, err := readScaffoldFile(absolute)
		if err != nil {
			return nil, err
		}
		if file.Action == "preserve" {
			contents[file.Path] = before
		}
		after, ok := contents[file.Path]
		if !ok {
			return nil, fmt.Errorf("missing scaffold content for %q", file.Path)
		}
		result = append(result, evolution.SourceEdit{Path: file.Path, BeforeDigest: digestBytes(before), After: append([]byte(nil), after...), BeforeExists: exists, AfterExists: true, Mode: uint32(mode)})
	}
	return result, nil
}

func readScaffoldFile(path string) ([]byte, bool, os.FileMode, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, 0o644, nil
	}
	if err != nil {
		return nil, false, 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, 0, fmt.Errorf("assistant scaffold target %q is not a regular file", filepath.ToSlash(path))
	}
	data, err := os.ReadFile(path)
	return data, true, info.Mode().Perm(), err
}

func ensureAssistantEvalDirectory(root, relative string) error {
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := rejectExistingAssistantPathSymlinks(root, path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("assistant eval path %q is not a directory", relative)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(path, 0o755)
}

func rejectExistingAssistantPathSymlinks(root, target string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	for current := target; ; current = filepath.Dir(current) {
		_, statErr := os.Lstat(current)
		if statErr == nil {
			return scn.RejectPathSymlinks(root, current)
		}
		if !os.IsNotExist(statErr) {
			return statErr
		}
		if current == root || current == filepath.Dir(current) {
			return nil
		}
	}
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validAssistantName(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' || character == '_' || index > 0 && character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}

const scaffoldAgentSource = `import { defineAgent } from "eve";

export default defineAgent({
  instructions: "You are a Scenery assistant.",
});
`

const scaffoldInstructions = `# Scenery assistant

Describe the assistant's behavior here. This file is authored content and is
never overwritten by Scenery.
`
