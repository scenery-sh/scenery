package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/atomicfile"
)

type nativeReloadOutput struct {
	mu sync.Mutex
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *nativeReloadOutput) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(data)
	if len(data) > b.limit-b.Len() {
		data, b.truncated = data[:b.limit-b.Len()], true
	}
	_, _ = b.Buffer.Write(data)
	return n, nil
}

type nativeReloadCommandRecord struct {
	CWD               string   `json:"cwd"`
	Argv              []string `json:"argv"`
	DurationMS        float64  `json:"duration_ms"`
	Error             string   `json:"error,omitempty"`
	Output            string   `json:"output_artifact,omitempty"`
	UserMS            float64  `json:"user_ms,omitempty"`
	SystemMS          float64  `json:"system_ms,omitempty"`
	started, finished time.Time
}

// CommandContext retains the existing process-tree cancellation mechanism.
// All output, including failures and full go-list captures, has an explicit cap.
func nativeReloadCommand(ctx context.Context, cwd string, env []string, outputPath string, program string, args ...string) ([]byte, nativeReloadCommandRecord, error) {
	command := commandTreeContext(ctx, program, args...)
	command.Dir, command.Env = cwd, env
	output := &nativeReloadOutput{limit: 32 << 20}
	command.Stdout, command.Stderr = output, output
	started := time.Now()
	err := command.Run()
	finished := time.Now()
	record := nativeReloadCommandRecord{CWD: cwd, Argv: append([]string{program}, args...), DurationMS: nativeReloadMS(finished.Sub(started)), Output: outputPath, started: started, finished: finished}
	if command.ProcessState != nil {
		record.UserMS = nativeReloadMS(command.ProcessState.UserTime())
		record.SystemMS = nativeReloadMS(command.ProcessState.SystemTime())
	}
	if output.truncated {
		err = errors.Join(err, fmt.Errorf("command output exceeded 32 MiB"))
	}
	if outputPath != "" {
		err = errors.Join(err, atomicfile.Write(outputPath, output.Bytes(), 0o600, atomicfile.Options{}))
	}
	if err != nil {
		record.Error = err.Error()
	}
	return bytes.Clone(output.Bytes()), record, err
}

func nativeReloadMS(duration time.Duration) float64 { return float64(duration.Nanoseconds()) / 1e6 }

func nativeReloadWriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(path, append(data, '\n'), 0o600, atomicfile.Options{})
}

type nativeReloadPackage struct {
	ImportPath                                                                                 string
	Dir                                                                                        string
	Imports                                                                                    []string
	ImportMap                                                                                  map[string]string
	GoFiles, CgoFiles, CFiles, CXXFiles, MFiles, HFiles, FFiles, SFiles, SysoFiles, EmbedFiles []string
	IgnoredGoFiles, IgnoredOtherFiles                                                          []string
	Incomplete                                                                                 bool
	Error                                                                                      json.RawMessage
	DepsErrors                                                                                 []json.RawMessage
	Module                                                                                     *struct {
		GoMod   string
		Replace *struct{ GoMod string }
	}
}

type nativeReloadInputSet struct {
	Digest      string            `json:"digest"`
	Files       map[string]string `json:"files"`
	Packages    []string          `json:"packages"`
	Environment json.RawMessage   `json:"go_environment"`
}

func nativeReloadCapture(data []byte, goEnvironment json.RawMessage, appRoot string) (nativeReloadInputSet, error) {
	set := nativeReloadInputSet{Files: map[string]string{}, Environment: goEnvironment}
	var packages []nativeReloadPackage
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var pkg nativeReloadPackage
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return set, err
		}
		if pkg.ImportPath == "" || pkg.Incomplete || len(pkg.Error) != 0 || len(pkg.DepsErrors) != 0 {
			return set, fmt.Errorf("incomplete package capture: %s", pkg.ImportPath)
		}
		packages = append(packages, pkg)
		set.Packages = append(set.Packages, pkg.ImportPath)
	}
	slices.Sort(set.Packages)
	if len(set.Packages) == 0 || slices.Contains(set.Packages, "scenery.sh/runtime") || slices.Contains(set.Packages, "clean.tech/internal/scenerygen/composition") {
		return set, fmt.Errorf("missing island closure or full runtime/composition retained")
	}
	addFile := func(path string) error {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("input is not a regular file: %s: %v", path, err)
		}
		digest, err := nativeReloadFileDigest(path)
		if err == nil {
			set.Files[path] = digest
		}
		return err
	}
	for _, pkg := range packages {
		for _, dependency := range pkg.Imports {
			if mapped := pkg.ImportMap[dependency]; mapped != "" {
				dependency = mapped
			}
			if dependency != "C" && !slices.Contains(set.Packages, dependency) {
				return set, fmt.Errorf("capture omitted %s imported by %s", dependency, pkg.ImportPath)
			}
		}
		for _, files := range [][]string{pkg.GoFiles, pkg.CgoFiles, pkg.CFiles, pkg.CXXFiles, pkg.MFiles, pkg.HFiles, pkg.FFiles, pkg.SFiles, pkg.SysoFiles, pkg.EmbedFiles, pkg.IgnoredGoFiles, pkg.IgnoredOtherFiles} {
			for _, file := range files {
				if err := addFile(filepath.Join(pkg.Dir, file)); err != nil {
					return set, err
				}
			}
		}
		if pkg.Module != nil {
			moduleFile := pkg.Module.GoMod
			if pkg.Module.Replace != nil {
				moduleFile = pkg.Module.Replace.GoMod
			}
			if moduleFile != "" {
				if err := addFile(moduleFile); err != nil {
					return set, err
				}
			}
		}
	}
	for _, name := range []string{"go.mod", "go.sum", "app.scn", "app.lock.scn", ".scenery.json", "solar/ahjs/package.scn"} {
		if err := addFile(filepath.Join(appRoot, name)); err != nil {
			return set, err
		}
	}
	encoded, err := json.Marshal(set)
	set.Digest = nativeReloadDigest(encoded)
	return set, err
}

func nativeReloadContractABI(appRoot string) (string, error) {
	path := filepath.Join(appRoot, "solar/ahjs/scenerycontract/contract.gen.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return "", err
	}
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		for _, spec := range group.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != "PackageContractABIRevision" || len(value.Values) != 1 {
				continue
			}
			literal, ok := value.Values[0].(*ast.BasicLit)
			if ok && literal.Kind == token.STRING {
				return strconv.Unquote(literal.Value)
			}
		}
	}
	return "", fmt.Errorf("generated package contract ABI constant is absent")
}

func nativeReloadEditedSource(original []byte, behavior string) ([]byte, error) {
	anchor := []byte(`"query must be at most %d characters"`)
	if bytes.Count(original, anchor) != 1 || strings.ContainsAny(behavior, "\"\n\r%") {
		return nil, fmt.Errorf("unexpected AHJ implementation source or behavior marker")
	}
	return bytes.Replace(original, anchor, []byte(strconv.Quote("query must be at most %d characters ["+behavior+"]")), 1), nil
}

type nativeReloadToolAction struct {
	Mode            string   `json:"mode"`
	Package         string   `json:"package"`
	CommandMS       float64  `json:"command_ms"`
	CommandUserMS   float64  `json:"command_user_ms"`
	CommandSystemMS float64  `json:"command_system_ms"`
	Command         []string `json:"command"`
	ActionMS        float64  `json:"action_ms"`
	QueueMS         float64  `json:"queue_ms"`
}

// The current stock Go tool exposes command and enclosing action intervals in
// its diagnostic action graph. Keep raw graphs and fail if attribution is absent.
func nativeReloadToolActions(data []byte) ([]nativeReloadToolAction, error) {
	var graph []struct {
		Mode, Package                  string
		Cmd                            []string
		CmdReal, CmdUser, CmdSys       time.Duration
		TimeReady, TimeStart, TimeDone time.Time
	}
	if err := json.Unmarshal(data, &graph); err != nil {
		return nil, err
	}
	var actions []nativeReloadToolAction
	for _, action := range graph {
		if len(action.Cmd) == 0 {
			continue
		}
		if action.CmdReal <= 0 || action.TimeDone.Before(action.TimeStart) {
			return nil, fmt.Errorf("missing Go action timing for %s", action.Package)
		}
		queue := time.Duration(0)
		if !action.TimeReady.IsZero() {
			queue = action.TimeStart.Sub(action.TimeReady)
		}
		actions = append(actions, nativeReloadToolAction{Mode: action.Mode, Package: action.Package, CommandMS: nativeReloadMS(action.CmdReal),
			Command: slices.Clone(action.Cmd), CommandUserMS: nativeReloadMS(action.CmdUser), CommandSystemMS: nativeReloadMS(action.CmdSys),
			ActionMS: nativeReloadMS(action.TimeDone.Sub(action.TimeStart)), QueueMS: nativeReloadMS(queue)})
	}
	if len(actions) == 0 {
		return nil, fmt.Errorf("go action graph contains no executed tool commands")
	}
	return actions, nil
}
