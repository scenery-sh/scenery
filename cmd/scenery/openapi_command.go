package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/openapiprojection"
)

const openAPIExportPayloadKind = "scenery.openapi.export"

type openAPIExportOptions struct {
	AppRoot          string
	Gateway          string
	Format           string
	OutputPath       string
	OutputMode       string
	IncludeFramework bool
	Check            bool
}

type openAPIExportGatewayPayload struct {
	Gateway             string         `json:"gateway"`
	Name                string         `json:"name"`
	BasePath            string         `json:"base_path"`
	OpenAPIVersion      string         `json:"openapi_version"`
	OpenAPIRevision     string         `json:"openapi_revision"`
	HTTPSurfaceRevision string         `json:"http_surface_revision"`
	Operations          int            `json:"operations"`
	ContentDigest       string         `json:"content_digest"`
	OutputPath          string         `json:"output_path,omitempty"`
	Document            map[string]any `json:"document,omitempty"`
}

type openAPIExportPayload struct {
	cliPayloadIdentity
	Format         string                        `json:"format"`
	OpenAPIVersion string                        `json:"openapi_version"`
	Checked        bool                          `json:"checked"`
	Changed        []string                      `json:"changed"`
	Gateways       []openAPIExportGatewayPayload `json:"gateways"`
}

func parseOpenAPIArgs(args []string) (openAPIExportOptions, error) {
	opts := openAPIExportOptions{Format: "json", OutputMode: "human"}
	flags := newCLIFlagSet("openapi")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Gateway, "gateway", "", "")
	flags.StringVar(&opts.Format, "format", opts.Format, "")
	flags.StringVar(&opts.OutputPath, "output", "", "")
	flags.BoolVar(&opts.IncludeFramework, "include-framework", false, "")
	flags.StringVar(&opts.OutputMode, "o", opts.OutputMode, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return openAPIExportOptions{}, err
	}
	if len(positionals) > 0 {
		return openAPIExportOptions{}, usageErrorf("unexpected argument %q", positionals[0])
	}
	if err := validateOpenAPIOptions(opts, false); err != nil {
		return openAPIExportOptions{}, err
	}
	return opts, nil
}

func parseGenerateOpenAPIArgs(args []string) (openAPIExportOptions, error) {
	opts := openAPIExportOptions{Format: "json", OutputMode: "human"}
	flags := newCLIFlagSet("generate openapi")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Gateway, "gateway", "", "")
	flags.StringVar(&opts.Format, "format", opts.Format, "")
	flags.StringVar(&opts.OutputPath, "output", "", "")
	flags.BoolVar(&opts.IncludeFramework, "include-framework", false, "")
	flags.BoolVar(&opts.Check, "check", false, "")
	flags.StringVar(&opts.OutputMode, "o", opts.OutputMode, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return openAPIExportOptions{}, err
	}
	if len(positionals) > 0 {
		return openAPIExportOptions{}, usageErrorf("unexpected argument %q", positionals[0])
	}
	if err := validateOpenAPIOptions(opts, true); err != nil {
		return openAPIExportOptions{}, err
	}
	return opts, nil
}

func validateOpenAPIOptions(opts openAPIExportOptions, requireOutput bool) error {
	if err := validateContractOutput(opts.OutputMode); err != nil {
		return err
	}
	if opts.Format != "json" && opts.Format != "yaml" {
		return usageErrorf("unsupported OpenAPI format %q; use json or yaml", opts.Format)
	}
	if requireOutput && strings.TrimSpace(opts.OutputPath) == "" {
		return usageErrorf("generate openapi requires --output <path>")
	}
	return nil
}

func runOpenAPIExport(stdout io.Writer, args []string) error {
	opts, err := parseOpenAPIArgs(args)
	if err != nil {
		return err
	}
	return executeOpenAPIExport(stdout, opts)
}

func runGenerateOpenAPI(stdout io.Writer, args []string) error {
	opts, err := parseGenerateOpenAPIArgs(args)
	if err != nil {
		return err
	}
	return executeOpenAPIExport(stdout, opts)
}

func executeOpenAPIExport(stdout io.Writer, opts openAPIExportOptions) error {
	compiled, err := compileContractRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	if !compiled.Valid() {
		return writeSourceResult(stdout, opts.OutputMode, false, compiled, map[string]any{
			"contract_status": compiled.ContractStatus,
		})
	}
	docs, err := openapiprojection.Project(
		compiled.Manifest,
		compiled.FrameworkResources,
		compiled.HTTPSurfaceRevisions,
		compiled.OpenAPIRevisions,
		openapiprojection.Options{
			Gateway:          opts.Gateway,
			IncludeFramework: opts.IncludeFramework,
		},
	)
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return usageErrorf("no HTTP gateways found in application")
	}

	encoded := make([][]byte, len(docs))
	for i, doc := range docs {
		var data []byte
		if opts.Format == "yaml" {
			data, err = openapiprojection.EncodeYAML(doc.Document)
		} else {
			data, err = openapiprojection.EncodeJSON(doc.Document)
		}
		if err != nil {
			return err
		}
		encoded[i] = data
	}

	var outputPaths []string
	if strings.TrimSpace(opts.OutputPath) != "" {
		outputPaths, err = resolveOpenAPIOutputPaths(opts.OutputPath, opts.Format, docs)
		if err != nil {
			return err
		}
	} else if opts.OutputMode == "human" && len(docs) > 1 {
		names := make([]string, len(docs))
		for i, doc := range docs {
			names[i] = doc.GatewayName
		}
		return usageErrorf("multiple HTTP gateways found (%s); pass --gateway <name> or --output <dir>", strings.Join(names, ", "))
	}

	changed := make([]string, 0)
	for i, path := range outputPaths {
		existing, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(existing, encoded[i]) {
			changed = append(changed, filepath.ToSlash(path))
			if !opts.Check {
				if err := writeFileAtomically(path, encoded[i]); err != nil {
					return err
				}
			}
		}
	}

	var staleErr error
	var staleDiag graph.Diagnostic
	if opts.Check && len(changed) > 0 {
		staleErr = fmt.Errorf("generated OpenAPI output is out of date: %s", strings.Join(changed, ", "))
		staleDiag = graph.Diagnostic{Code: "SCN6204", Severity: "error", Message: staleErr.Error()}
	}

	if opts.OutputMode == "json" {
		gateways := make([]openAPIExportGatewayPayload, len(docs))
		for i, doc := range docs {
			item := openAPIExportGatewayPayload{
				Gateway:             doc.GatewayAddress,
				Name:                doc.GatewayName,
				BasePath:            doc.BasePath,
				OpenAPIVersion:      doc.OpenAPIVersion,
				OpenAPIRevision:     doc.OpenAPIRevision,
				HTTPSurfaceRevision: doc.HTTPSurfaceRevision,
				Operations:          doc.Operations,
				ContentDigest:       openapiprojection.ContentDigest(encoded[i]),
			}
			if len(outputPaths) > 0 {
				item.OutputPath = filepath.ToSlash(outputPaths[i])
			} else {
				item.Document = doc.Document
			}
			gateways[i] = item
		}
		payload := openAPIExportPayload{
			cliPayloadIdentity: newCLIPayloadIdentity(openAPIExportPayloadKind),
			Format:             opts.Format,
			OpenAPIVersion:     openapiprojection.OpenAPIVersion,
			Checked:            opts.Check,
			Changed:            changed,
			Gateways:           gateways,
		}
		envelope := newOpenAPIEnvelope(compiled, payload, staleDiag, staleErr == nil)
		if err := json.NewEncoder(stdout).Encode(envelope); err != nil {
			return err
		}
		if staleErr != nil {
			return &silentCLIError{err: findingsDiagnosticError(staleErr, staleDiag), code: 1}
		}
		return nil
	}

	if staleErr != nil {
		return findingsDiagnosticError(staleErr, staleDiag)
	}
	if len(outputPaths) == 0 {
		_, err = stdout.Write(encoded[0])
		return err
	}
	if opts.Check {
		_, _ = fmt.Fprintln(stdout, "scenery: generated OpenAPI documents are current")
		return nil
	}
	for i, path := range outputPaths {
		_, _ = fmt.Fprintf(stdout, "scenery: wrote %s (%s, %d operations)\n", filepath.ToSlash(path), docs[i].GatewayName, docs[i].Operations)
	}
	return nil
}

func newOpenAPIEnvelope(compiled *compiler.Result, payload openAPIExportPayload, extraDiag graph.Diagnostic, ok bool) any {
	envelope := newCLIEnvelope(ok, payload, nil)
	if compiled != nil {
		envelope.WorkspaceRevision = compiled.WorkspaceRevision
		if compiled.Manifest != nil {
			envelope.ContractRevision = compiled.Manifest.ContractRevision
		}
		if len(compiled.ImplementationRevisions) > 0 {
			envelope.ImplementationRevision = compiled.ImplementationRevisions
		}
		envelope.Diagnostics = append(envelope.Diagnostics, compiled.Diagnostics...)
	}
	if extraDiag.Code != "" {
		envelope.Diagnostics = append(envelope.Diagnostics, extraDiag)
	}
	return envelope
}

func resolveOpenAPIOutputPaths(rawOutput, format string, docs []openapiprojection.DocumentResult) ([]string, error) {
	trimmed := strings.TrimSpace(rawOutput)
	trailingSep := strings.HasSuffix(trimmed, "/") || strings.HasSuffix(trimmed, string(filepath.Separator))
	cleaned := filepath.Clean(trimmed)
	info, statErr := os.Stat(cleaned)
	isDir := trailingSep || (statErr == nil && info.IsDir())

	if len(docs) == 1 && !isDir {
		return []string{cleaned}, nil
	}
	if len(docs) > 1 && !isDir {
		ext := strings.ToLower(filepath.Ext(cleaned))
		if ext == ".json" || ext == ".yaml" || ext == ".yml" {
			names := make([]string, len(docs))
			for i, doc := range docs {
				names[i] = doc.GatewayName
			}
			return nil, usageErrorf("multiple HTTP gateways found (%s); pass --gateway <name> or a directory to --output", strings.Join(names, ", "))
		}
	}
	paths := make([]string, len(docs))
	for i, doc := range docs {
		paths[i] = filepath.Join(cleaned, doc.GatewayName+".openapi."+format)
	}
	return paths, nil
}

func writeFileAtomically(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".openapi-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
