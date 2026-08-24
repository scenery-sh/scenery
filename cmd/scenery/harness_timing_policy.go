package main

import (
	"fmt"
	"strings"
	"unicode"
)

const (
	harnessTestClassFast                = "fast"
	harnessTestClassIntegration         = "integration"
	harnessFastTestTargetSeconds        = 0.060
	harnessFastTestBudgetSeconds        = 0.100
	harnessIntegrationTestTargetSeconds = 0.100
	harnessIntegrationTestBudgetSeconds = 3.000
	harnessTimingConfirmationPercentile = 95
)

// harnessTestTimingException is an exact, reviewable exception to the fast
// in-process test-body budget. Exceptions name one top-level Go test root and
// the external boundary that makes repeated process, toolchain, service, or OS
// work part of that test's assertion. Package, prefix, regexp, and subtest
// exemptions are intentionally unsupported.
type harnessTestTimingException struct {
	Package        string  `json:"package"`
	Name           string  `json:"name"`
	Class          string  `json:"class"`
	TargetSeconds  float64 `json:"target_seconds"`
	BudgetSeconds  float64 `json:"budget_seconds"`
	BoundaryReason string  `json:"classification_reason"`
}

// harnessTimingIntegrationExceptionPolicy is kept in package/name order so a
// timing report contains a stable, complete exception inventory. Tests that
// merely happen to be slow stay in the default fast class.
var harnessTimingIntegrationExceptionPolicy = []harnessTestTimingException{
	{Package: "scenery.sh", Name: "TestInstalledSceneryBinaryMatchesRepoUsesBuildInfoWithTrimpath", BoundaryReason: "builds and executes a real Go binary to verify installed build metadata"},

	{Package: "scenery.sh/cmd/scenery", Name: "TestAssistantInitAppliesScaffold", BoundaryReason: "runs the full production initializer through predicted Go and TypeScript generation checks, crash-safe evolution plan/apply, and durable atomic filesystem publication"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestBeginManagedFrontendBackendsReturnsBeforeReady", BoundaryReason: "starts and stops a real frontend child process and waits on an OS listener"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestCLIProcessExitStatusMatchesEdition2027Contract", BoundaryReason: "executes real CLI helper processes because OS exit status and per-process telemetry persistence cannot be observed in-process"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestCleanupStaleDevSessionProcessesStopsStateRootMatchedOrphans", BoundaryReason: "starts, discovers, signals, and reaps real OS processes"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestCleanupSupersededDevSessionsStopsSameSessionChildren", BoundaryReason: "starts, fingerprints, signals, and reaps real OS processes while verifying session-scoped child cleanup"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestCleanupSymphonyRunWorkspaceRemovesWorktree", BoundaryReason: "creates and removes a real Git worktree"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestDeploySSHChildProcessExitCodePropagates", BoundaryReason: "executes a real child process to preserve exec.ExitError and exit-code behavior"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestDesktopShellExitDoesNotRestart", BoundaryReason: "starts and observes a real desktop shell child process through the agent service"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestDesktopShellUsesFrontendBackendAndRegistersProcess", BoundaryReason: "starts a real desktop shell child process and exercises the agent Unix socket"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestDevManagedProcessStartupTimeoutIncludesLastProbeAndTail", BoundaryReason: "starts, probes, signals, and reaps a real managed child process while verifying timeout diagnostics"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestDevNamedLockSecondProcessTimesOutWithNamedError", BoundaryReason: "starts a competing process and exercises the OS advisory-lock boundary"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestEnsureSharedVictoriaStackReplacesStaleOwner", BoundaryReason: "starts and replaces real Victoria component processes and listeners"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestEnsureSharedVictoriaStackReusesAgentSubstrate", BoundaryReason: "starts a real local agent, child owner process, and listeners to verify live Victoria substrate reuse"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestEnsureSharedVictoriaStackSerializesConcurrentStarts", BoundaryReason: "starts real Victoria component processes behind cross-process locks"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestFollowAlreadyRunningDevSessionExitsWhenOwnerStops", BoundaryReason: "starts and observes a real dev-session owner process"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestInspectDocsGoPackagesForPath", BoundaryReason: "invokes the real Go package loader/toolchain for path ownership"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestManagedFrontendExitRestartsAndUpdatesAgentSession", BoundaryReason: "restarts a real frontend child process and updates a live agent socket"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestPrepareDevAgentSessionRegistersOnceWithFrontendBackends", BoundaryReason: "runs a real local agent service over a Unix socket"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestPrepareSymphonyWorkspaceResetsExistingWorktree", BoundaryReason: "creates and resets a real Git worktree"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestProbeHarnessToolParsesSceneryVersionJSON", BoundaryReason: "executes a real helper binary and decodes its machine response"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestRunSceneryScriptRunsGoFileFromAppRoot", BoundaryReason: "invokes the real Go toolchain to run an authored Go file"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestRunUpgradeInstallsVerifiedReleaseAndSyncsToolchain", BoundaryReason: "executes real installer and toolchain synchronization child commands"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestSnapshotBackupScriptVerifiesReplicatesThenPrunes", BoundaryReason: "executes the generated Bash backup script and filesystem tools"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestStopDeletedSessionProcessesStopsOwner", BoundaryReason: "starts, signals, and reaps a real OS owner process"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestStopDeletedSessionProcessesStopsStateRootMatchedOrphan", BoundaryReason: "starts, discovers, signals, and reaps a real orphan process"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestVictoriaRecoverySerializesConcurrentAttempts", BoundaryReason: "runs real Victoria component processes, listeners, and recovery locks"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestVictoriaRecoveryStopsWithSupervisor", BoundaryReason: "runs and stops real Victoria component processes with the supervisor"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestVictoriaSupervisorRecoversExitedComponent", BoundaryReason: "kills and recovers a real Victoria component process"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestWorktreeCreateDoesNotEnsureDatabaseBranch", BoundaryReason: "creates and removes a real Git worktree"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestWorktreeCreateListAndRemoveWithoutDBPin", BoundaryReason: "creates, lists, and removes a real Git worktree"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestWorktreeCreateSkipsDBPinForManualBranchPolicy", BoundaryReason: "creates and removes a real Git worktree"},
	{Package: "scenery.sh/cmd/scenery", Name: "TestWorktreeRemoveRestoresDBStateWhenGitRemoveFails", BoundaryReason: "invokes real Git worktree removal and its failure recovery"},

	{Package: "scenery.sh/internal/agent", Name: "TestAgentRestartPreservesSubstratesAndRoutes", BoundaryReason: "restarts the real local agent service with Unix sockets, TCP routing, and child substrates"},

	{Package: "scenery.sh/internal/build", Name: "TestBuildInputManifestIncludesLocalReplaceBytes", BoundaryReason: "invokes the real Go package loader across a local module replacement"},
	{Package: "scenery.sh/internal/build", Name: "TestCompileRealGoBuildSmoke", BoundaryReason: "invokes the real Go compiler as the retained build-boundary smoke test"},
	{Package: "scenery.sh/internal/build", Name: "TestPrepareAndCompileNativeContractApplication", BoundaryReason: "generates, compiles, starts, and probes a real Go application and Bun frontend"},
	{Package: "scenery.sh/internal/build", Name: "TestPrepareAndCompileWriteLatestBuildManifest", BoundaryReason: "invokes the real Go compiler and writes the build manifest"},

	{Package: "scenery.sh/internal/desktop", Name: "TestRunStreamsOutputAndPreservesExitCode", BoundaryReason: "executes a real child process to verify streams and exit codes"},

	{Package: "scenery.sh/internal/edge", Name: "TestCaddyValidateGeneratedStaticConfig", BoundaryReason: "invokes a real Caddy binary to validate generated configuration"},
	{Package: "scenery.sh/internal/edge", Name: "TestReloadInvokesCaddy", BoundaryReason: "starts a real process at the Caddy admin boundary"},
	{Package: "scenery.sh/internal/edge", Name: "TestStartReportsFastStartupExit", BoundaryReason: "starts and observes a real managed edge process"},
	{Package: "scenery.sh/internal/edge", Name: "TestStartWritesRunningStateAndStopTerminatesProcess", BoundaryReason: "starts, signals, and reaps a real managed edge process"},
	{Package: "scenery.sh/internal/edge", Name: "TestTrustLocalCAUsesTemporaryAdmin", BoundaryReason: "starts real HTTP and helper processes on temporary OS listeners"},

	{Package: "scenery.sh/internal/generate", Name: "TestApplyImplementationCheckReportsValidNative", BoundaryReason: "invokes the real Go compiler to validate generated native implementation"},
	{Package: "scenery.sh/internal/generate", Name: "TestEditorWorkspaceExplicitMergePreservesUserWorkFile", BoundaryReason: "invokes the real Go toolchain to prove merged user and generated workspace modules resolve and compile"},
	{Package: "scenery.sh/internal/generate", Name: "TestEditorWorkspaceSupportsRawGoWithoutMaterializedGeneratedGo", BoundaryReason: "invokes the real Go toolchain against a staged editor workspace"},
	{Package: "scenery.sh/internal/generate", Name: "TestGenerateBootstrapsContractArtifactsWhileImplementationIsInvalid", BoundaryReason: "invokes the real Go compiler across invalid and generated implementation states"},
	{Package: "scenery.sh/internal/generate", Name: "TestGeneratedProviderCRUDAdapterCompilesInCleanClone", BoundaryReason: "runs real generation and Go compilation in a clean copied workspace"},
	{Package: "scenery.sh/internal/generate", Name: "TestNativeImplementationVerificationUsesOverlayWithoutGeneratedTree", BoundaryReason: "invokes the real Go compiler with a generated source overlay"},
	{Package: "scenery.sh/internal/generate", Name: "TestNestedExportedTypeGeneratesCompilableGoContractClosure", BoundaryReason: "invokes the real Go compiler to prove a generated type closure compiles"},
	{Package: "scenery.sh/internal/generate", Name: "TestRenderAssistantAssetRegistryGeneratedPackageCompiles", BoundaryReason: "invokes the real Go compiler to prove the generated embedded assistant asset registry package compiles"},
	{Package: "scenery.sh/internal/generate", Name: "TestVerifyImplementationResolvesGeneratedLibraryFacade", BoundaryReason: "invokes the real Go compiler to resolve the generated library facade"},

	{Package: "scenery.sh/internal/testsuite", Name: "TestRunCachesLinkedBinariesAndExecutesTestsFresh", BoundaryReason: "links and executes real Go test binaries through the fresh-test runner"},
	{Package: "scenery.sh/internal/testsuite", Name: "TestWorkspaceFingerprintChangesForDirtyTrackedSource", BoundaryReason: "creates a real Git repository and queries tracked workspace state"},

	{Package: "scenery.sh/internal/toolchain", Name: "TestStoreSyncSourceBuildArtifact", BoundaryReason: "invokes the real Go compiler to build and verify a source-defined toolchain artifact"},

	{Package: "scenery.sh/internal/tscheck", Name: "TestCheckClassifiesGeneratedAndApplicationDiagnostics", BoundaryReason: "invokes the real TypeScript compiler and classifies its diagnostics"},
	{Package: "scenery.sh/internal/tscheck", Name: "TestCheckRedirectsSceneryUIAliasToStagedCatalog", BoundaryReason: "invokes the real TypeScript compiler against a staged catalog"},

	{Package: "scenery.sh/internal/validation", Name: "TestValidateChangedCollectsPathsRelativeToAppRoot", BoundaryReason: "creates and queries a real Git repository for changed paths"},

	{Package: "scenery.sh/internal/victoria", Name: "TestStartVictoriaComponentsAttributesStartErrors", BoundaryReason: "starts real component child processes and attributes OS startup failures"},

	{Package: "scenery.sh/runtime", Name: "TestProductionInstallsStartsReusesAndRejectsTamperedAssets", BoundaryReason: "allocates real loopback listeners and starts, signals, and reaps production assistant child processes while verifying durable asset install and tamper recovery"},
}

func harnessTimingIntegrationExceptions() []harnessTestTimingException {
	exceptions := append([]harnessTestTimingException(nil), harnessTimingIntegrationExceptionPolicy...)
	for i := range exceptions {
		exceptions[i].Class = harnessTestClassIntegration
		exceptions[i].TargetSeconds = harnessIntegrationTestTargetSeconds
		exceptions[i].BudgetSeconds = harnessIntegrationTestBudgetSeconds
	}
	if err := validateHarnessTimingIntegrationExceptions(exceptions); err != nil {
		panic(err)
	}
	return exceptions
}

func harnessTimingIntegrationException(packageName, testName string, exceptions []harnessTestTimingException) (harnessTestTimingException, bool) {
	for _, exception := range exceptions {
		if exception.Package == packageName && exception.Name == testName {
			return exception, true
		}
	}
	return harnessTestTimingException{}, false
}

func validateHarnessTimingIntegrationExceptions(exceptions []harnessTestTimingException) error {
	previous := ""
	for i, exception := range exceptions {
		identity := exception.Package + "." + exception.Name
		if strings.TrimSpace(exception.Package) == "" {
			return fmt.Errorf("integration timing exception %d has an empty package", i)
		}
		if strings.ContainsAny(exception.Package, "*?[]()|^$\\") || strings.Contains(exception.Package, "...") {
			return fmt.Errorf("integration timing exception %q is not an exact package and test identity", identity)
		}
		if !isExactTopLevelGoTestRoot(exception.Name) {
			return fmt.Errorf("integration timing exception %q is not an exact top-level TestX root", identity)
		}
		if strings.TrimSpace(exception.BoundaryReason) == "" {
			return fmt.Errorf("integration timing exception %q has no boundary reason", identity)
		}
		if exception.Class != harnessTestClassIntegration {
			return fmt.Errorf("integration timing exception %q has class %q", identity, exception.Class)
		}
		if exception.TargetSeconds != harnessIntegrationTestTargetSeconds || exception.BudgetSeconds != harnessIntegrationTestBudgetSeconds {
			return fmt.Errorf("integration timing exception %q has target/budget %.3f/%.3f, want %.3f/%.3f", identity, exception.TargetSeconds, exception.BudgetSeconds, harnessIntegrationTestTargetSeconds, harnessIntegrationTestBudgetSeconds)
		}
		if previous != "" && identity <= previous {
			return fmt.Errorf("integration timing exceptions must be unique and sorted: %q follows %q", identity, previous)
		}
		previous = identity
	}
	return nil
}

func isExactTopLevelGoTestRoot(name string) bool {
	if !strings.HasPrefix(name, "Test") || len(name) == len("Test") || strings.Contains(name, "/") {
		return false
	}
	for i, r := range name[len("Test"):] {
		if i == 0 && unicode.IsLower(r) {
			return false
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
