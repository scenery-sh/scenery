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
	"sync"
	"time"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/compiler"
	generateapi "scenery.sh/internal/generate/api"
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
	// Identity is the build identity of the whole generation: the runtime
	// bundle of the development target, which a single application executable
	// built from the same inputs links. Every process of the set was linked
	// from, or has the process identity of, this build.
	Identity      DevelopmentProcessIdentity
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
// The returned join completes the implementation check, which runs beside the
// build; the caller must call it before using the checked contract, and may
// prepare the linked executables meanwhile.
func BuildDevelopmentProcessesContext(ctx context.Context, result *Result) (*DevelopmentProcessSet, func() error, error) {
	if result == nil || result.Contract == nil || result.Contract.Manifest == nil || result.Target == nil {
		return nil, nil, fmt.Errorf("development processes require a prepared contract and target")
	}
	if result.Target.Role != "development" || result.Ephemeral || result.ProductionAssets {
		return nil, nil, fmt.Errorf("development processes require an ordinary development target")
	}
	if err := requireGenerateHooks(); err != nil {
		return nil, nil, err
	}
	// The plan reads the contract only, so it is made while the workspace is
	// verified and its build inputs are read; every return joins it.
	planned := make(chan struct{})
	var plan generateapi.RuntimeIntegrationPlan
	var planErr error
	contract := result.Contract
	go func() {
		defer close(planned)
		planStarted := time.Now()
		plan, planErr = generateHooks.RuntimeIntegrationPlan(contract)
		RecordStep(ctx, Step{Name: "process.plan", StartedAt: planStarted, Duration: time.Since(planStarted), Cache: "not_applicable", Reason: "runtime_integration_plan", OK: planErr == nil, Actions: len(plan.Services)})
	}()
	defer func() { <-planned }()
	// A held preparation established the workspace's membership and bytes
	// under the lock it hands over, so nothing can have changed them since;
	// otherwise the lock is taken again and the workspace verified.
	unlock := result.takeWorkspaceHold()
	held := unlock != nil
	if !held {
		var err error
		unlock, err = lockWorkspace(result.Dir)
		if err != nil {
			return nil, nil, err
		}
	}
	unlocked := false
	defer func() {
		if !unlocked {
			unlock()
		}
	}()
	verifyWorkspace := result.verification != nil
	if verifyWorkspace && !held {
		if err := observeWorkspaceVerification(ctx, result, "before_compile"); err != nil {
			return nil, nil, err
		}
	}
	if result.NeedsTidy {
		if err := tidyWorkspace(ctx, result); err != nil {
			return nil, nil, err
		}
	}
	var set *DevelopmentProcessSet
	check, err := compileBesidePreparedVerification(ctx, result, func(ctx context.Context) error {
		var buildErr error
		set, buildErr = buildDevelopmentProcesses(ctx, result, func() ([]generateapi.ServiceProcessPlan, error) {
			<-planned
			return plan.Services, planErr
		})
		return buildErr
	})
	if err != nil {
		return nil, nil, err
	}
	// The linked executables are returned before the workspace is verified
	// again, so the caller retains and preflights them meanwhile; the join
	// verifies the workspace and records the build before anything of it is
	// published. The workspace stays locked until the implementation check,
	// which reads it, has joined.
	unlocked = true
	return set, func() error {
		defer unlock()
		if err := completeDevelopmentProcessBuild(ctx, result, verifyWorkspace); err != nil {
			return errors.Join(err, check())
		}
		if err := check(); err != nil {
			return err
		}
		result.verification = nil
		// The runtime bundle describes a verified build only.
		return writeRuntimeBundle(result)
	}, nil
}

// completeDevelopmentProcessBuild proves, after linking, that the workspace
// and owned module sources still hold the bytes the build identity names, and
// records the compiled workspace.
func completeDevelopmentProcessBuild(ctx context.Context, result *Result, verifyWorkspace bool) error {
	if verifyWorkspace {
		if err := observeWorkspaceVerification(ctx, result, "after_compile"); err != nil {
			return err
		}
	}
	if err := VerifyOwnedGoModuleSourcesContext(ctx, result.OwnedGoModuleSources); err != nil {
		return err
	}
	if err := savePrimedWorkspace(result); err != nil {
		return err
	}
	return WriteLatestBuildManifest(result, "compiled")
}

func buildDevelopmentProcesses(ctx context.Context, result *Result, planned func() ([]generateapi.ServiceProcessPlan, error)) (*DevelopmentProcessSet, error) {
	manifest, err := buildInputManifest(ctx, result)
	if err != nil {
		return nil, err
	}
	services, err := planned()
	if err != nil {
		return nil, err
	}
	names := []string{DevelopmentProcessHost}
	bindingOwners := map[string]string{}
	for _, service := range services {
		names = append(names, service.Name)
		for _, address := range service.RequiredAddresses {
			if strings.Contains(address, "/binding/") {
				bindingOwners[address] = service.Name
			}
		}
	}
	identityStarted := time.Now()
	mains := make([]string, len(names))
	digests := make([]string, len(names))
	for index, name := range names {
		digest, main, err := manifest.developmentProcessDigest(name)
		if err != nil {
			return nil, err
		}
		mains[index], digests[index] = main, digest
	}
	// The target and the host implement the application contract and share
	// one projection. A service process implements its service contract, so a
	// contract change elsewhere leaves its identity, and with unchanged build
	// inputs its executable, as they were.
	revisions, diagnostics := compiler.ImplementationRevisionsForInputs(result.Contract, result.Target.Name, []string{manifest.Digest, digests[0]})
	resources := make(map[string]compiler.Resource, len(result.Contract.Manifest.Resources))
	for _, resource := range result.Contract.Manifest.Resources {
		resources[resource.Address] = resource
	}
	serviceProcesses := make([]compiler.ServiceProcess, len(services))
	for index, service := range services {
		serviceProcesses[index] = compiler.ServiceProcess{Service: resources[service.Address], Covered: service.RequiredAddresses, ContractRevision: service.ContractRevision, InputDigest: digests[index+1]}
	}
	serviceRevisions, serviceDiagnostics := compiler.ServiceProcessImplementationRevisions(result.Contract, result.Target.Name, serviceProcesses)
	for _, diagnostic := range append(diagnostics, serviceDiagnostics...) {
		if diagnostic.Severity == "error" {
			return nil, fmt.Errorf("%s: %s", diagnostic.Code, diagnostic.Message)
		}
	}
	targetRevision := revisions[manifest.Digest]
	if targetRevision == "" {
		return nil, fmt.Errorf("implementation_revision is unavailable for Go target %s", result.Target.Name)
	}
	result.BuildInput, result.ImplementationRevisions = manifest, map[string]string{result.Target.Name: targetRevision}
	set := &DevelopmentProcessSet{BindingOwners: bindingOwners, Identity: DevelopmentProcessIdentity{
		ContractRevision: result.Contract.Manifest.ContractRevision, ImplementationRevision: targetRevision,
		BuildInputDigest: manifest.Digest, GoTarget: result.Target.Name,
	}}
	identities := make([]DevelopmentProcessIdentity, len(names))
	identities[0] = DevelopmentProcessIdentity{ContractRevision: result.Contract.Manifest.ContractRevision, ImplementationRevision: revisions[digests[0]]}
	for index, service := range services {
		identities[index+1] = DevelopmentProcessIdentity{ContractRevision: service.ContractRevision, ImplementationRevision: serviceRevisions[service.Address]}
	}
	RecordStep(ctx, Step{Name: "process.identity", StartedAt: identityStarted, Duration: time.Since(identityStarted), Cache: "not_applicable", Reason: "entrypoint_import_closures", OK: true, Actions: len(names)})
	binaryRoot := filepath.Join(result.Dir, developmentProcessBinaryDir)
	if err := os.MkdirAll(binaryRoot, 0o755); err != nil {
		return nil, err
	}
	reuseStarted := time.Now()
	var pending []*DevelopmentProcess
	processes := make([]*DevelopmentProcess, 0, len(names))
	for index, name := range names {
		process := &DevelopmentProcess{Name: name, Package: mains[index], Identity: DevelopmentProcessIdentity{
			ContractRevision: identities[index].ContractRevision, ImplementationRevision: identities[index].ImplementationRevision,
			BuildInputDigest: digests[index], GoTarget: result.Target.Name,
		}}
		if process.Identity.ImplementationRevision == "" {
			return nil, fmt.Errorf("implementation_revision is unavailable for development process %s", name)
		}
		key, err := developmentProcessKey(process, result.GoBuildFlags)
		if err != nil {
			return nil, err
		}
		process.Binary = filepath.Join(binaryRoot, name+"-"+key)
		if digest, ok, err := retainedDevelopmentProcessDigest(process.Binary); err != nil {
			return nil, err
		} else if ok {
			process.ArtifactDigest = digest
		} else {
			pending = append(pending, process)
		}
		processes = append(processes, process)
	}
	RecordStep(ctx, Step{Name: "process.reuse", StartedAt: reuseStarted, Duration: time.Since(reuseStarted), Cache: "retained_digest", Reason: "linked_identity_unchanged", OK: true, Actions: len(names) - len(pending), CacheMisses: len(pending)})
	if len(pending) > 0 {
		if err := linkDevelopmentProcesses(ctx, result, binaryRoot, pending); err != nil {
			return nil, err
		}
	}
	retireCompilerState(ctx, result.Dir)
	keep := map[string]bool{}
	result.DevelopmentProcessBinaries = result.DevelopmentProcessBinaries[:0]
	for _, process := range processes {
		result.DevelopmentProcessBinaries = append(result.DevelopmentProcessBinaries, filepath.Join(developmentProcessBinaryDir, filepath.Base(process.Binary)))
		keep[filepath.Base(process.Binary)] = true
		keep[filepath.Base(process.Binary)+developmentProcessDigestSuffix] = true
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

// linkDevelopmentProcesses runs one stock Go build for every pending process
// under the host-wide fair link slot. Each entrypoint receives its own linker
// metadata through a package-scoped -ldflags value; outputs are published only
// after the whole build succeeds.
func linkDevelopmentProcesses(ctx context.Context, result *Result, binaryRoot string, pending []*DevelopmentProcess) error {
	generation, err := os.MkdirTemp(binaryRoot, ".link-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(generation) }()
	release, err := developmentLinkSlot(ctx, result)
	if err != nil {
		return err
	}
	err = runGoContextWithEnvironment(ctx, result.Dir, result.GoEnvironment, developmentProcessBuildArgs(result.GoBuildFlags, generation, pending)...)
	release()
	if err != nil {
		return err
	}
	for _, process := range pending {
		output := filepath.Join(generation, filepath.Base(process.Package))
		started := time.Now()
		digest, size, err := developmentProcessFileDigest(output)
		if err != nil {
			return fmt.Errorf("development process %s was not linked: %w", process.Name, err)
		}
		if err := os.Rename(output, process.Binary); err != nil {
			return err
		}
		if err := publishDevelopmentProcessDigest(process.Binary, digest); err != nil {
			return err
		}
		process.ArtifactDigest = digest
		RecordStep(ctx, Step{Name: "build.artifact", StartedAt: started, Duration: time.Since(started), Cache: "miss", Reason: "linked_development_process_" + process.Name, OK: true, ExecutableBytes: size})
	}
	return nil
}

// developmentProcessBuildArgs keeps the configured Go build flags, moves every
// -ldflags value (both -ldflags=value and the -ldflags value pair) into each
// entrypoint's package-scoped linker flags, and names the pending entrypoints.
// developmentLinkSlot waits for the host-wide fair link slot that orders the
// links of every worktree's development builds.
func developmentLinkSlot(ctx context.Context, result *Result) (func(), error) {
	root, err := sharedBinaryRoot()
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(result.Dir))
	return acquireSharedBinarySlotObserved(ctx, root, hex.EncodeToString(digest[:]))
}

func developmentProcessBuildArgs(buildFlags []string, output string, pending []*DevelopmentProcess) []string {
	flags := withRuntimeBundleLinkerMetadata(normalizeGoBuildFlags(buildFlags), "", nil)
	args := append([]string{"build"}, flags[:len(flags)-1]...)
	for _, process := range pending {
		linker := withRuntimeBundleLinkerMetadata(normalizeGoBuildFlags(buildFlags), developmentLinkerFlags, map[string]string{
			"scenery.sh/runtime.linkedContractRevision":       process.Identity.ContractRevision,
			"scenery.sh/runtime.linkedImplementationRevision": process.Identity.ImplementationRevision,
			"scenery.sh/runtime.linkedBuildInputDigest":       process.Identity.BuildInputDigest,
			"scenery.sh/runtime.linkedGoTarget":               process.Identity.GoTarget,
		})
		args = append(args, "-ldflags="+process.Package+"="+strings.TrimPrefix(linker[len(linker)-1], "-ldflags="))
	}
	args = append(args, "-buildvcs=false", "-o", output+string(filepath.Separator))
	for _, process := range pending {
		args = append(args, process.Package)
	}
	return args
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

// developmentProcessFileDigest reads a whole executable; tests observe it.
var developmentProcessFileDigest = fileDigest

// developmentProcessDigestSuffix names the record of the digest an executable
// had when its link published it, beside the executable. The executable's path
// is keyed by the linked identity, so the record binds that identity to the
// verified output.
const developmentProcessDigestSuffix = ".sha256"

// developmentProcessDigests remembers the verified digest of each linked process
// executable for as long as its file keeps the same size, modification and
// change times, permissions, device and inode, so a one-service edit does not
// read the executables of unchanged processes.
var developmentProcessDigests struct {
	sync.Mutex
	values map[string]developmentProcessDigest
}

type developmentProcessDigest struct {
	stamp  buildInputFileStamp
	digest string
}

// publishDevelopmentProcessDigest records the digest a link produced for an
// executable it has just published.
func publishDevelopmentProcessDigest(path, digest string) error {
	if err := atomicfile.Write(path+developmentProcessDigestSuffix, []byte(digest+"\n"), 0o600, atomicfile.Options{}); err != nil {
		return err
	}
	return rememberDevelopmentProcessDigest(path, digest)
}

func rememberDevelopmentProcessDigest(path, digest string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	developmentProcessDigests.Lock()
	defer developmentProcessDigests.Unlock()
	if developmentProcessDigests.values == nil {
		developmentProcessDigests.values = map[string]developmentProcessDigest{}
	}
	developmentProcessDigests.values[path] = developmentProcessDigest{stamp: buildInputStamp(info), digest: digest}
	return nil
}

// retainedDevelopmentProcessDigest reports the verified digest of an already
// linked process executable. A remembered current stamp avoids reading it;
// otherwise its bytes must still have the digest its link published. An
// executable without that record, or whose bytes differ, is not reusable and is
// linked again: rehashing observes content, it does not establish which output
// was verified.
func retainedDevelopmentProcessDigest(path string) (string, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("development process executable is not a regular file: %s", path)
	}
	developmentProcessDigests.Lock()
	remembered, ok := developmentProcessDigests.values[path]
	developmentProcessDigests.Unlock()
	if ok && remembered.stamp == buildInputStamp(info) {
		return remembered.digest, true, nil
	}
	record, err := os.ReadFile(path + developmentProcessDigestSuffix)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	expected := strings.TrimSpace(string(record))
	digest, _, err := developmentProcessFileDigest(path)
	if err != nil {
		return "", false, err
	}
	if expected == "" || digest != expected {
		return "", false, nil
	}
	return digest, true, rememberDevelopmentProcessDigest(path, digest)
}
