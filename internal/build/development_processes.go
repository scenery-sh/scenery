package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/compiler"
	generateapi "scenery.sh/internal/generate/api"
	"scenery.sh/internal/nativebuilddriver"
)

// developmentProcessBinaryDir holds the linked executables of process-model
// development generations inside the private workspace.
const developmentProcessBinaryDir = "scenery-processes"

// DevelopmentProcessHost names the host process of a process-model generation.
const DevelopmentProcessHost = "host"

// DevelopmentProcessIdentity is the runtime identity linked into one
// development process.
type DevelopmentProcessIdentity struct {
	ContractRevision       string
	ImplementationRevision string
	BuildInputDigest       string
	GoTarget               string
}

// DevelopmentProcess is one executable of a process-model generation.
type DevelopmentProcess struct {
	Name           string
	Package        string
	Binary         string
	ArtifactDigest string
	Identity       DevelopmentProcessIdentity
}

// DevelopmentProcessSet is a complete process-model generation: the host, one
// process per native service, and the service process owning each binding.
type DevelopmentProcessSet struct {
	Host          DevelopmentProcess
	Services      []DevelopmentProcess
	BindingOwners map[string]string
	// Rebuilt names the processes this build linked instead of reusing.
	Rebuilt []string
}

// Process returns the named process of the set.
func (set *DevelopmentProcessSet) Process(name string) (DevelopmentProcess, bool) {
	if set == nil {
		return DevelopmentProcess{}, false
	}
	if name == DevelopmentProcessHost {
		return set.Host, set.Host.Name != ""
	}
	for _, process := range set.Services {
		if process.Name == name {
			return process, true
		}
	}
	return DevelopmentProcess{}, false
}

// BuildDevelopmentProcessesContext links the process entrypoints of a prepared
// development workspace. A process whose linked identity equals a retained
// executable is reused; every other process is linked by one stock Go build.
func BuildDevelopmentProcessesContext(ctx context.Context, result *Result) (*DevelopmentProcessSet, error) {
	if result == nil || result.Contract == nil || result.Contract.Manifest == nil || result.Target == nil {
		return nil, fmt.Errorf("development processes require a prepared contract and target")
	}
	if result.Target.Role != "development" || result.Ephemeral || result.ProductionAssets {
		return nil, fmt.Errorf("development processes require an ordinary development target")
	}
	if err := requireGenerateHooks(); err != nil {
		return nil, err
	}
	plan, err := generateHooks.RuntimeIntegrationPlan(result.Contract)
	if err != nil {
		return nil, err
	}
	if len(plan.Services) == 0 {
		return nil, fmt.Errorf("development processes require at least one native service")
	}
	unlock, err := lockWorkspace(result.Dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if result.NeedsTidy {
		if err := tidyWorkspace(ctx, result); err != nil {
			return nil, err
		}
	}
	var set *DevelopmentProcessSet
	err = compileWithPreparedVerification(ctx, result, func(ctx context.Context) error {
		var buildErr error
		set, buildErr = buildDevelopmentProcesses(ctx, result, plan.Services)
		return buildErr
	})
	if err != nil {
		return nil, err
	}
	if err := VerifyOwnedGoModuleSourcesContext(ctx, result.OwnedGoModuleSources); err != nil {
		return nil, err
	}
	if err := savePrimedWorkspace(result); err != nil {
		return nil, err
	}
	if err := WriteLatestBuildManifest(result, "compiled"); err != nil {
		return nil, err
	}
	return set, nil
}

func buildDevelopmentProcesses(ctx context.Context, result *Result, services []generateapi.ServiceProcessPlan) (*DevelopmentProcessSet, error) {
	manifest, err := buildInputManifest(ctx, result)
	if err != nil {
		return nil, err
	}
	names := []string{DevelopmentProcessHost}
	set := &DevelopmentProcessSet{BindingOwners: map[string]string{}}
	for _, service := range services {
		names = append(names, service.Name)
		for _, address := range service.RequiredAddresses {
			if strings.Contains(address, "/binding/") {
				set.BindingOwners[address] = service.Name
			}
		}
	}
	binaryRoot := filepath.Join(result.Dir, developmentProcessBinaryDir)
	if err := os.MkdirAll(binaryRoot, 0o755); err != nil {
		return nil, err
	}
	var pending []*DevelopmentProcess
	processes := make([]*DevelopmentProcess, 0, len(names))
	for _, name := range names {
		processManifest, main, err := manifest.developmentProcessManifest(name)
		if err != nil {
			return nil, err
		}
		revisions, diagnostics := compiler.ComputeImplementationRevisions(result.Contract, map[string]string{result.Target.Name: processManifest.Digest})
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == "error" {
				return nil, fmt.Errorf("%s: %s", diagnostic.Code, diagnostic.Message)
			}
		}
		process := &DevelopmentProcess{Name: name, Package: main, Identity: DevelopmentProcessIdentity{
			ContractRevision: result.Contract.Manifest.ContractRevision, ImplementationRevision: revisions[result.Target.Name],
			BuildInputDigest: processManifest.Digest, GoTarget: result.Target.Name,
		}}
		if process.Identity.ImplementationRevision == "" {
			return nil, fmt.Errorf("implementation_revision is unavailable for development process %s", name)
		}
		key, err := developmentProcessKey(process, result.GoBuildFlags)
		if err != nil {
			return nil, err
		}
		process.Binary = filepath.Join(binaryRoot, name+"-"+key)
		if info, statErr := os.Lstat(process.Binary); statErr == nil && info.Mode().IsRegular() {
			if process.ArtifactDigest, _, err = nativebuilddriver.FileDigest(process.Binary); err != nil {
				return nil, err
			}
		} else if !errors.Is(statErr, os.ErrNotExist) && statErr != nil {
			return nil, statErr
		} else {
			pending = append(pending, process)
		}
		processes = append(processes, process)
	}
	if len(pending) > 0 {
		if err := linkDevelopmentProcesses(ctx, result, binaryRoot, pending); err != nil {
			return nil, err
		}
	}
	keep := map[string]bool{}
	for _, process := range processes {
		keep[filepath.Base(process.Binary)] = true
	}
	for _, process := range pending {
		set.Rebuilt = append(set.Rebuilt, process.Name)
	}
	if err := pruneDevelopmentProcessBinaries(binaryRoot, keep); err != nil {
		return nil, err
	}
	set.Host = *processes[0]
	for _, process := range processes[1:] {
		set.Services = append(set.Services, *process)
	}
	return set, nil
}

func developmentProcessKey(process *DevelopmentProcess, buildFlags []string) (string, error) {
	encoded, err := json.Marshal(struct {
		Package    string
		Identity   DevelopmentProcessIdentity
		BuildFlags []string
		Linker     string
	}{process.Package, process.Identity, buildFlags, developmentLinkerFlags})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:8]), nil
}

// linkDevelopmentProcesses runs one stock Go build for every pending process.
// Each entrypoint receives its own linker metadata through a package-scoped
// -ldflags value; outputs are published only after the whole build succeeds.
func linkDevelopmentProcesses(ctx context.Context, result *Result, binaryRoot string, pending []*DevelopmentProcess) error {
	generation, err := os.MkdirTemp(binaryRoot, ".link-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(generation) }()
	args := []string{"build"}
	for _, flag := range normalizeGoBuildFlags(result.GoBuildFlags) {
		if flag != "-ldflags" && !strings.HasPrefix(flag, "-ldflags=") {
			args = append(args, flag)
		}
	}
	for _, process := range pending {
		flags := withRuntimeBundleLinkerMetadata(result.GoBuildFlags, developmentLinkerFlags, map[string]string{
			"scenery.sh/runtime.linkedContractRevision":       process.Identity.ContractRevision,
			"scenery.sh/runtime.linkedImplementationRevision": process.Identity.ImplementationRevision,
			"scenery.sh/runtime.linkedBuildInputDigest":       process.Identity.BuildInputDigest,
			"scenery.sh/runtime.linkedGoTarget":               process.Identity.GoTarget,
		})
		args = append(args, "-ldflags="+process.Package+"="+strings.TrimPrefix(flags[len(flags)-1], "-ldflags="))
	}
	args = append(args, "-buildvcs=false", "-o", generation+string(filepath.Separator))
	for _, process := range pending {
		args = append(args, process.Package)
	}
	if err := runGoContextWithEnvironment(ctx, result.Dir, result.GoEnvironment, args...); err != nil {
		return err
	}
	for _, process := range pending {
		output := filepath.Join(generation, filepath.Base(process.Package))
		started := time.Now()
		digest, size, err := nativebuilddriver.FileDigest(output)
		if err != nil {
			return fmt.Errorf("development process %s was not linked: %w", process.Name, err)
		}
		if err := os.Rename(output, process.Binary); err != nil {
			return err
		}
		process.ArtifactDigest = digest
		RecordStep(ctx, Step{Name: "build.artifact", StartedAt: started, Duration: time.Since(started), Cache: "miss", Reason: "linked_development_process_" + process.Name, OK: true, ExecutableBytes: size})
	}
	return nil
}

func pruneDevelopmentProcessBinaries(root string, keep map[string]bool) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if keep[entry.Name()] || strings.HasPrefix(entry.Name(), ".link-") {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
