package compiler

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	"scenery.sh/internal/scn"
	"scenery.sh/internal/workspacetx"
)

// BuiltinProviderLockPlan refreshes only declared builtin providers. Compilation
// stays offline and never invokes this explicit source-edit workflow.
type BuiltinProviderLockPlan struct {
	Before    []byte
	After     []byte
	Exists    bool
	Mode      uint32
	Providers []LockEntry
}

func PlanBuiltinProviderLocks(root string) (BuiltinProviderLockPlan, error) {
	plan := BuiltinProviderLockPlan{Mode: 0o644, Providers: []LockEntry{}}
	if err := workspacetx.RecoverOrReject(root, workspacetx.NormalRead); err != nil {
		return plan, err
	}
	lock, diagnostics, err := loadLockfile(root)
	if err != nil {
		return plan, fmt.Errorf("failed_precondition: read app.lock.scn: %w", err)
	}
	if len(diagnostics) != 0 {
		return plan, fmt.Errorf("failed_precondition: invalid app.lock.scn: %s", diagnostics[0].Message)
	}
	if lock != nil {
		plan.Before, plan.Exists, plan.Mode = lock.source, true, uint32(lock.mode)
	}
	files, err := scn.SourceFiles(root, true)
	if err != nil {
		return plan, fmt.Errorf("failed_precondition: read provider declarations: %w", err)
	}
	if err := scn.RejectPathSymlinks(root, filepath.Join(root, scn.AppFilename)); err != nil {
		return plan, fmt.Errorf("failed_precondition: %w", err)
	}
	if _, err := os.Stat(filepath.Join(root, scn.AppFilename)); err != nil {
		return plan, fmt.Errorf("failed_precondition: %w", err)
	}
	declared := map[string]LockEntry{}
	for _, path := range files {
		source, diagnostics := scn.Parse(root, path)
		if len(diagnostics) != 0 {
			return plan, fmt.Errorf("failed_precondition: %s: %s", filepath.Base(path), diagnostics[0].Message)
		}
		for _, block := range source.Blocks {
			if block.Type != "provider" {
				continue
			}
			identity, ok := scn.LiteralString(block, "source")
			if !ok || len(block.Labels) != 1 || !sceneryIdentifierPattern.MatchString(block.Labels[0]) {
				return plan, fmt.Errorf("invalid_request: provider requires one lower_snake label and a literal source")
			}
			entry, builtin := BuiltinProviderLockEntry(block.Labels[0], identity)
			if !builtin {
				if _, pinned := lock.find("provider", identity); !pinned {
					return plan, fmt.Errorf("capability_unavailable: provider %s is not bundled; supply its immutable lock and cache explicitly (provider lock performs no downloads)", identity)
				}
				continue
			}
			// Multiple local declarations may share one immutable provider source.
			if previous, exists := declared[identity]; exists && previous.Name < entry.Name {
				continue
			}
			declared[identity] = entry
		}
	}
	for _, entry := range declared {
		plan.Providers = append(plan.Providers, entry)
	}
	slices.SortFunc(plan.Providers, func(a, b LockEntry) int { return strings.Compare(a.Source, b.Source) })
	if len(plan.Providers) == 0 {
		plan.After = plan.Before
		return plan, nil
	}
	data := plan.Before
	if !plan.Exists {
		data = []byte("lock {}\n")
	}
	file, parseDiags := hclsyntax.ParseConfig(data, scn.AppLockFilename, hcl.InitialPos)
	if parseDiags.HasErrors() {
		return plan, fmt.Errorf("failed_precondition: invalid app.lock.scn: %s", parseDiags.Error())
	}
	var output bytes.Buffer
	cursor := 0
	pending := append([]LockEntry(nil), plan.Providers...)
	for _, block := range file.Body.(*hclsyntax.Body).Blocks {
		if block.Type == "lock" {
			continue
		}
		attribute := block.Body.Attributes["source"]
		if attribute == nil || len(block.Labels) != 1 {
			return plan, fmt.Errorf("revision_conflict: app.lock.scn changed while planning")
		}
		value, valueDiagnostics := attribute.Expr.Value(nil)
		if valueDiagnostics.HasErrors() || value.IsNull() || !value.IsKnown() || value.Type() != cty.String {
			return plan, fmt.Errorf("failed_precondition: lock source must be a literal string")
		}
		key := block.Type + "\x00" + value.AsString()
		start, end := block.Range().Start.Byte, block.Range().End.Byte
		output.Write(data[cursor:start])
		for len(pending) > 0 && "provider\x00"+pending[0].Source < key {
			output.Write(renderProviderLock(nil, pending[0]))
			output.WriteString("\n\n")
			pending = pending[1:]
		}
		if len(pending) > 0 && "provider\x00"+pending[0].Source == key {
			entry := pending[0]
			// A lock label is local metadata; preserve it and all unrelated fields.
			entry.Name = block.Labels[0]
			output.Write(renderProviderLock(data[start:end], entry))
			pending = pending[1:]
		} else {
			output.Write(data[start:end])
		}
		cursor = end
	}
	output.Write(data[cursor:])
	for _, entry := range pending {
		output.WriteString("\n")
		output.Write(renderProviderLock(nil, entry))
		output.WriteString("\n")
	}
	plan.After = output.Bytes()
	return plan, nil
}

func renderProviderLock(before []byte, entry LockEntry) []byte {
	file := hclwrite.NewEmptyFile()
	var block *hclwrite.Block
	if len(before) != 0 {
		file, _ = hclwrite.ParseConfig(before, scn.AppLockFilename, hcl.InitialPos)
		block = file.Body().Blocks()[0]
	} else {
		block = file.Body().AppendNewBlock("provider", []string{entry.Name})
	}
	for _, field := range []struct{ name, value string }{
		{"source", entry.Source}, {"integrity", entry.Integrity},
		{"compile_descriptor_digest", entry.CompileDescriptorDigest},
		{"runtime_abi", entry.RuntimeABI}, {"deployment_abi", entry.DeploymentABI},
		{"migration_abi", entry.MigrationABI},
	} {
		if field.value != "" {
			block.Body().SetAttributeValue(field.name, cty.StringVal(field.value))
		} else {
			block.Body().RemoveAttribute(field.name)
		}
	}
	return bytes.TrimSuffix(file.Bytes(), []byte("\n"))
}
