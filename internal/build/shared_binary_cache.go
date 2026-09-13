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
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"scenery.sh/internal/machine"
)

const (
	sharedBinaryKind                    = "scenery.shared-development-binary"
	sharedBinarySchemaDescriptor        = `{"binary_sha256":"digest","binary_size":"integer","build_input_digest":"digest","contract_revision":"digest","framework_source_digest":"digest","go_build_flags":"array<string>","implementation_revision":"digest","key":"digest","kind":"scenery.shared-development-binary","producer":"producer","runtime_linker_metadata":"map<string,string>","schema_revision":"digest","spec_revision":"digest","target":"go-target"}`
	sharedBinaryCacheVersion            = "v1"
	sharedBinaryCacheEntries            = 64
	sharedBinaryCacheBytes        int64 = 4 << 30
	sharedBinaryLinkSlots               = 2
	sharedBinarySubscriberPoll          = 10 * time.Millisecond
	sharedBinaryRegistrationGrace       = time.Minute
)

var sharedBinarySubscriberSequence atomic.Uint64

type sharedBinaryArtifact struct {
	machine.ArtifactIdentity
	Key                    string            `json:"key"`
	Target                 string            `json:"target"`
	ContractRevision       string            `json:"contract_revision"`
	ImplementationRevision string            `json:"implementation_revision"`
	BuildInputDigest       string            `json:"build_input_digest"`
	FrameworkSourceDigest  string            `json:"framework_source_digest"`
	GoBuildFlags           []string          `json:"go_build_flags"`
	RuntimeLinkerMetadata  map[string]string `json:"runtime_linker_metadata"`
	BinarySHA256           string            `json:"binary_sha256"`
	BinarySize             int64             `json:"binary_size"`
}

type sharedBinaryKeyInput struct {
	Producer               machine.Producer   `json:"producer"`
	Target                 sharedBinaryTarget `json:"target"`
	ContractRevision       string             `json:"contract_revision"`
	ImplementationRevision string             `json:"implementation_revision"`
	BuildInputDigest       string             `json:"build_input_digest"`
	FrameworkSourceDigest  string             `json:"framework_source_digest"`
	GoBuildFlags           []string           `json:"go_build_flags"`
	RuntimeLinkerMetadata  map[string]string  `json:"runtime_linker_metadata"`
}

type sharedBinaryTarget struct {
	Name                 string              `json:"name"`
	Address              string              `json:"address"`
	Role                 string              `json:"role"`
	ModuleRoot           string              `json:"module_root"`
	Patterns             []string            `json:"patterns"`
	ToolchainVersion     string              `json:"toolchain_version"`
	GOOS                 string              `json:"goos"`
	GOARCH               string              `json:"goarch"`
	CGOEnabled           bool                `json:"cgo_enabled"`
	Experiments          []string            `json:"experiments"`
	BuildTags            []string            `json:"build_tags"`
	BuildFlags           []string            `json:"build_flags"`
	ArchitectureEnv      map[string]string   `json:"architecture_env"`
	NativeToolEnv        map[string]string   `json:"native_tool_env"`
	ToolchainIdentity    map[string]string   `json:"toolchain_identity"`
	NativeToolIdentities []map[string]string `json:"native_tool_identities"`
}

func normalizedSharedBinaryTarget(result *Result) (sharedBinaryTarget, error) {
	target := result.Target
	moduleRoot, err := filepath.Abs(target.Context.ModuleRoot)
	if err != nil {
		return sharedBinaryTarget{}, fmt.Errorf("resolve build target module root: %w", err)
	}
	return sharedBinaryTarget{
		Name: target.Name, Address: target.Address, Role: target.Role, ModuleRoot: filepath.ToSlash(moduleRoot),
		Patterns: append([]string(nil), target.Context.Patterns...), ToolchainVersion: target.Context.ToolchainVersion,
		GOOS: target.Context.GOOS, GOARCH: target.Context.GOARCH, CGOEnabled: target.Context.CGOEnabled,
		Experiments: append([]string(nil), target.Context.Experiments...), BuildTags: append([]string(nil), target.Context.BuildTags...),
		BuildFlags: append([]string(nil), target.Context.BuildFlags...), ArchitectureEnv: target.Context.ArchitectureEnv,
		NativeToolEnv: target.Context.NativeToolEnv, ToolchainIdentity: target.Context.ToolchainIdentity,
		NativeToolIdentities: target.Context.NativeToolIdentities,
	}, nil
}

func sharedBinaryKey(result *Result) (string, sharedBinaryArtifact, error) {
	if result == nil || result.Target == nil || result.Contract == nil || result.Contract.Manifest == nil || result.BuildInput == nil {
		return "", sharedBinaryArtifact{}, fmt.Errorf("shared binary cache requires complete target and build input identity")
	}
	implementation := result.ImplementationRevisions[result.Target.Name]
	if implementation == "" || result.BuildInput.Digest == "" || len(result.RuntimeLinkerMetadata) == 0 {
		return "", sharedBinaryArtifact{}, fmt.Errorf("shared binary cache requires implementation and linker identity")
	}
	flags := normalizeGoBuildFlags(result.GoBuildFlags)
	metadata := make(map[string]string, len(result.RuntimeLinkerMetadata))
	for key, value := range result.RuntimeLinkerMetadata {
		metadata[key] = value
	}
	target, err := normalizedSharedBinaryTarget(result)
	if err != nil {
		return "", sharedBinaryArtifact{}, err
	}
	input := sharedBinaryKeyInput{
		Producer: machine.RuntimeProducer(), Target: target, ContractRevision: result.Contract.Manifest.ContractRevision,
		ImplementationRevision: implementation, BuildInputDigest: result.BuildInput.Digest,
		FrameworkSourceDigest: result.FrameworkSourceDigest, GoBuildFlags: flags, RuntimeLinkerMetadata: metadata,
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", sharedBinaryArtifact{}, err
	}
	sum := sha256.Sum256(encoded)
	key := hex.EncodeToString(sum[:])
	return key, sharedBinaryArtifact{
		ArtifactIdentity: machine.NewArtifactIdentity(sharedBinaryKind, sharedBinarySchemaDescriptor),
		Key:              key, Target: result.Target.Name, ContractRevision: result.Contract.Manifest.ContractRevision,
		ImplementationRevision: implementation, BuildInputDigest: result.BuildInput.Digest,
		FrameworkSourceDigest: result.FrameworkSourceDigest, GoBuildFlags: flags, RuntimeLinkerMetadata: metadata,
	}, nil
}

func sharedBinaryRoot() (string, error) {
	root, err := CacheRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "build", "shared-binaries", sharedBinaryCacheVersion), nil
}

func runSharedGoBuildContext(ctx context.Context, result *Result) error {
	if result == nil || result.Target == nil || result.Target.Role != "development" || result.Ephemeral || result.ProductionAssets {
		return runGoBuildContext(ctx, result)
	}
	key, expected, err := sharedBinaryKey(result)
	if err != nil {
		return err
	}
	root, err := sharedBinaryRoot()
	if err != nil {
		return err
	}
	if hit, err := restoreSharedBinary(root, key, expected, result.Binary); err != nil {
		return err
	} else if hit {
		return recordSharedBinaryStep(ctx, result.Binary, "hit", "complete_content_addressed_artifact", nil)
	}
	subscriber, err := subscribeSharedBinary(root, key)
	if err != nil {
		return err
	}
	defer subscriber.Close()

	waitStarted := time.Now()
	releaseAction, err := acquireSharedBinaryLock(ctx, filepath.Join(root, "locks", "action-"+key[:2]+".lock"))
	queue := time.Since(waitStarted)
	RecordStep(ctx, Step{Name: "build.shared_queue", StartedAt: waitStarted, Duration: queue, QueueDuration: queue, Cache: "not_applicable", Reason: "content_key", OK: err == nil})
	if err != nil {
		return err
	}
	if hit, err := restoreSharedBinary(root, key, expected, result.Binary); err != nil {
		releaseAction()
		return err
	} else if hit {
		releaseAction()
		return recordSharedBinaryStep(ctx, result.Binary, "hit", "joined_inflight_artifact", nil)
	}

	// The action lock transfers to the producer. Its cancellation follows the
	// complete cross-process subscriber set, not whichever caller happened to
	// win the action lock.
	producerContext, stopProducer := sharedBinaryProducerContext(ctx, subscriber.directory)
	produced := make(chan sharedBinaryProduction, 1)
	go func() {
		defer releaseAction()
		defer stopProducer()
		produced <- produceSharedBinary(producerContext, root, key, expected, result)
	}()

	select {
	case production := <-produced:
		if production.err != nil {
			return production.err
		}
		if production.published {
			hit, restoreErr := restoreSharedBinary(root, key, expected, result.Binary)
			if restoreErr != nil {
				return restoreErr
			}
			if !hit {
				return fmt.Errorf("shared binary disappeared after successful publication")
			}
			return nil
		}
		return writeExecutableAtomically(result.Binary, production.data)
	case <-ctx.Done():
		subscriber.Close()
		return ctx.Err()
	}
}

type sharedBinaryProduction struct {
	data      []byte
	published bool
	err       error
}

func produceSharedBinary(ctx context.Context, root, key string, expected sharedBinaryArtifact, result *Result) sharedBinaryProduction {
	waitStarted := time.Now()
	releaseSlot, err := acquireSharedBinarySlot(ctx, root, key)
	queue := time.Since(waitStarted)
	RecordStep(ctx, Step{Name: "build.shared_link_queue", StartedAt: waitStarted, Duration: queue, QueueDuration: queue, Cache: "not_applicable", Reason: "fair_link_slot", OK: err == nil})
	if err != nil {
		return sharedBinaryProduction{err: err}
	}
	defer releaseSlot()

	stage, releaseStage, err := createSharedBinaryStage(ctx, root)
	if err != nil {
		return sharedBinaryProduction{err: err}
	}
	defer func() {
		releaseStage()
		_ = os.RemoveAll(stage)
	}()
	staged := *result
	staged.Binary = filepath.Join(stage, "application")
	if err := runGoBuildContext(ctx, &staged); err != nil {
		_ = recordSharedBinaryStep(ctx, staged.Binary, "miss", "link_failed", err)
		return sharedBinaryProduction{err: err}
	}
	data, err := os.ReadFile(staged.Binary)
	if err != nil {
		return sharedBinaryProduction{err: err}
	}
	if err := publishSharedBinary(root, key, expected, staged.Binary); err != nil {
		_ = recordSharedBinaryStep(ctx, staged.Binary, "miss", "cache_publish_failed", err)
		return sharedBinaryProduction{data: data}
	}
	if err := pruneSharedBinaries(root, key); err != nil {
		_ = recordSharedBinaryStep(ctx, staged.Binary, "miss", "cache_prune_failed", err)
		return sharedBinaryProduction{data: data, published: true}
	}
	if err := recordSharedBinaryStep(ctx, staged.Binary, "miss", "linked_and_published", nil); err != nil {
		return sharedBinaryProduction{err: err}
	}
	return sharedBinaryProduction{data: data, published: true}
}

type sharedBinarySubscriber struct {
	directory string
	path      string
	release   func()
	once      sync.Once
}

func subscribeSharedBinary(root, key string) (*sharedBinarySubscriber, error) {
	directory := filepath.Join(root, "subscribers", key)
	name := fmt.Sprintf("%d-%d-%d.subscriber", os.Getpid(), time.Now().UnixNano(), sharedBinarySubscriberSequence.Add(1))
	path, release, err := createSharedBinaryLease(directory, name)
	if err != nil {
		return nil, err
	}
	return &sharedBinarySubscriber{directory: directory, path: path, release: release}, nil
}

func (s *sharedBinarySubscriber) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		s.release()
		_ = os.Remove(s.path)
		_ = os.Remove(s.directory)
	})
}

func sharedBinaryProducerContext(parent context.Context, subscriberDirectory string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(sharedBinarySubscriberPoll)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				active, err := activeSharedBinarySubscribers(subscriberDirectory, time.Now())
				if err == nil && active == 0 {
					cancel()
					return
				}
			}
		}
	}()
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			close(done)
			cancel()
		})
	}
}

func activeSharedBinarySubscribers(directory string, _ time.Time) (int, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	active := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".register-") {
			if err := cleanupAbandonedSharedBinaryRegistration(directory, entry, time.Now()); err != nil {
				return 0, err
			}
			continue
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".subscriber" {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		release, acquired, exists, lockErr := trySharedBinaryExistingLock(path)
		if lockErr != nil {
			return 0, lockErr
		}
		if !exists {
			continue
		}
		if !acquired {
			active++
			continue
		}
		release()
		_ = os.Remove(path)
	}
	if active == 0 {
		_ = os.Remove(directory)
	}
	return active, nil
}

func acquireSharedBinarySlot(ctx context.Context, root, key string) (func(), error) {
	ticketDirectory := filepath.Join(root, "queue")
	ticketName := fmt.Sprintf("%020d-%010d-%020d.ticket", time.Now().UnixNano(), os.Getpid(), sharedBinarySubscriberSequence.Add(1))
	ticketPath, releaseTicket, err := createSharedBinaryLease(ticketDirectory, ticketName)
	if err != nil {
		return nil, err
	}
	releaseTicketOnReturn := true
	defer func() {
		if releaseTicketOnReturn {
			releaseTicket()
			_ = os.Remove(ticketPath)
		}
	}()

	start := int(key[0]) % sharedBinaryLinkSlots
	for {
		position, err := sharedBinaryTicketPosition(ticketDirectory, filepath.Base(ticketPath))
		if err != nil {
			return nil, err
		}
		if position >= 0 && position < sharedBinaryLinkSlots {
			for offset := 0; offset < sharedBinaryLinkSlots; offset++ {
				path := filepath.Join(root, "slots", fmt.Sprintf("link-%d.lock", (start+offset)%sharedBinaryLinkSlots))
				if releaseSlot, acquired, err := trySharedBinaryLock(path); err != nil {
					return nil, err
				} else if acquired {
					releaseTicketOnReturn = false
					return func() {
						releaseSlot()
						releaseTicket()
						_ = os.Remove(ticketPath)
					}, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func createSharedBinaryLease(directory, name string) (string, func(), error) {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", nil, err
	}
	temporary, err := os.CreateTemp(directory, ".register-")
	if err != nil {
		return "", nil, err
	}
	temporaryPath := temporary.Name()
	release, acquired, err := trySharedBinaryFileLock(temporary)
	if err != nil {
		_ = os.Remove(temporaryPath)
		return "", nil, err
	}
	if !acquired {
		_ = os.Remove(temporaryPath)
		return "", nil, fmt.Errorf("lock unique shared binary lease")
	}
	path := filepath.Join(directory, name)
	if err := os.Rename(temporaryPath, path); err != nil {
		release()
		_ = os.Remove(temporaryPath)
		return "", nil, err
	}
	return path, release, nil
}

func sharedBinaryTicketPosition(directory, ticket string) (int, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return -1, nil
	}
	if err != nil {
		return -1, err
	}
	active := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".register-") {
			if err := cleanupAbandonedSharedBinaryRegistration(directory, entry, time.Now()); err != nil {
				return -1, err
			}
			continue
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".ticket" {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		release, acquired, exists, lockErr := trySharedBinaryExistingLock(path)
		if lockErr != nil {
			return -1, lockErr
		}
		if !exists {
			continue
		}
		if acquired {
			release()
			_ = os.Remove(path)
			continue
		}
		active = append(active, entry.Name())
	}
	sort.Strings(active)
	for index, name := range active {
		if name == ticket {
			return index, nil
		}
	}
	return -1, nil
}

func cleanupAbandonedSharedBinaryRegistration(directory string, entry os.DirEntry, now time.Time) error {
	if entry.IsDir() {
		return nil
	}
	info, err := entry.Info()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if now.Sub(info.ModTime()) < sharedBinaryRegistrationGrace {
		return nil
	}
	path := filepath.Join(directory, entry.Name())
	release, acquired, exists, err := trySharedBinaryExistingLock(path)
	if err != nil || !exists || !acquired {
		return err
	}
	release()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func createSharedBinaryStage(ctx context.Context, root string) (string, func(), error) {
	stagingRoot := filepath.Join(root, "staging")
	releaseCleanup, err := acquireSharedBinaryLock(ctx, filepath.Join(root, "locks", "staging-cleanup.lock"))
	if err != nil {
		return "", nil, err
	}
	defer releaseCleanup()
	if err := cleanupSharedBinaryStages(stagingRoot); err != nil {
		return "", nil, err
	}
	stage, err := os.MkdirTemp(stagingRoot, ".build-")
	if err != nil {
		return "", nil, err
	}
	release, acquired, err := trySharedBinaryLock(filepath.Join(stage, ".lease"))
	if err != nil || !acquired {
		_ = os.RemoveAll(stage)
		if err != nil {
			return "", nil, err
		}
		return "", nil, fmt.Errorf("lock shared binary staging directory")
	}
	return stage, release, nil
}

func cleanupSharedBinaryStages(stagingRoot string) error {
	entries, err := os.ReadDir(stagingRoot)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(stagingRoot, 0o755)
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ".build-") {
			continue
		}
		stage := filepath.Join(stagingRoot, entry.Name())
		release, acquired, exists, lockErr := trySharedBinaryExistingLock(filepath.Join(stage, ".lease"))
		if lockErr != nil {
			return lockErr
		}
		if exists && !acquired {
			continue
		}
		if acquired {
			release()
		}
		if err := os.RemoveAll(stage); err != nil {
			return err
		}
	}
	return nil
}

func acquireSharedBinaryLock(ctx context.Context, path string) (func(), error) {
	for {
		if release, acquired, err := trySharedBinaryLock(path); err != nil {
			return nil, err
		} else if acquired {
			return release, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func restoreSharedBinary(root, key string, expected sharedBinaryArtifact, destination string) (bool, error) {
	artifact, data, ok, err := loadSharedBinary(root, key)
	if err != nil || !ok {
		return false, err
	}
	if !sharedBinaryMetadataEqual(artifact, expected) {
		return false, nil
	}
	// A private workspace executable is reusable only after the current exact
	// shared manifest and its cached bytes have been validated above. Preserve
	// an already matching destination so an unchanged public restart neither
	// links nor replaces the inode; a bare destination can never reach this
	// branch by itself.
	if sharedBinaryDestinationMatches(destination, artifact) {
		return true, nil
	}
	if err := writeExecutableAtomically(destination, data); err != nil {
		return false, err
	}
	return true, nil
}

func sharedBinaryDestinationMatches(path string, artifact sharedBinaryArtifact) bool {
	if ok, err := regularArtifactPath(path); err != nil || !ok {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil || int64(len(data)) != artifact.BinarySize {
		return false
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) == artifact.BinarySHA256
}

func loadSharedBinary(root, key string) (sharedBinaryArtifact, []byte, bool, error) {
	dir := filepath.Join(root, "artifacts", key)
	if len(key) != 64 {
		return sharedBinaryArtifact{}, nil, false, nil
	}
	for _, char := range key {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return sharedBinaryArtifact{}, nil, false, nil
		}
	}
	manifestPath, binaryPath := filepath.Join(dir, "manifest.json"), filepath.Join(dir, "application")
	if ok, err := regularArtifactPath(manifestPath); err != nil || !ok {
		return sharedBinaryArtifact{}, nil, false, err
	}
	manifestData, err := os.ReadFile(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		return sharedBinaryArtifact{}, nil, false, nil
	}
	if err != nil {
		return sharedBinaryArtifact{}, nil, false, err
	}
	var artifact sharedBinaryArtifact
	if err := machine.DecodeArtifact(manifestData, &artifact, &artifact.ArtifactIdentity, sharedBinaryKind, sharedBinarySchemaDescriptor, "rebuild the shared development binary"); err != nil || artifact.Key != key || !reflect.DeepEqual(artifact.Producer, machine.RuntimeProducer()) {
		return sharedBinaryArtifact{}, nil, false, nil
	}
	if ok, err := regularArtifactPath(binaryPath); err != nil || !ok {
		return sharedBinaryArtifact{}, nil, false, err
	}
	data, err := os.ReadFile(binaryPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return sharedBinaryArtifact{}, nil, false, nil
		}
		return sharedBinaryArtifact{}, nil, false, err
	}
	sum := sha256.Sum256(data)
	if artifact.BinarySize != int64(len(data)) || artifact.BinarySHA256 != hex.EncodeToString(sum[:]) {
		return sharedBinaryArtifact{}, nil, false, nil
	}
	return artifact, data, true, nil
}

func regularArtifactPath(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular(), nil
}

func sharedBinaryMetadataEqual(actual, expected sharedBinaryArtifact) bool {
	return actual.Key == expected.Key && actual.Target == expected.Target &&
		actual.ContractRevision == expected.ContractRevision && actual.ImplementationRevision == expected.ImplementationRevision &&
		actual.BuildInputDigest == expected.BuildInputDigest && actual.FrameworkSourceDigest == expected.FrameworkSourceDigest &&
		reflect.DeepEqual(actual.GoBuildFlags, expected.GoBuildFlags) && reflect.DeepEqual(actual.RuntimeLinkerMetadata, expected.RuntimeLinkerMetadata)
}

func publishSharedBinary(root, key string, artifact sharedBinaryArtifact, source string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	artifact.BinarySHA256, artifact.BinarySize = hex.EncodeToString(sum[:]), int64(len(data))
	encoded, err := json.Marshal(artifact)
	if err != nil {
		return err
	}
	artifactsRoot := filepath.Join(root, "artifacts")
	if err := os.MkdirAll(artifactsRoot, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(artifactsRoot, ".publish-")
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := os.WriteFile(filepath.Join(stage, "application"), data, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "manifest.json"), encoded, 0o644); err != nil {
		return err
	}
	final := filepath.Join(artifactsRoot, key)
	if _, _, ok, loadErr := loadSharedBinary(root, key); loadErr != nil {
		return loadErr
	} else if ok {
		return nil
	}
	if err := os.RemoveAll(final); err != nil {
		return err
	}
	if err := os.Rename(stage, final); err != nil {
		return err
	}
	keep = true
	return nil
}

func writeExecutableAtomically(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".shared-binary-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o755); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func recordSharedBinaryStep(ctx context.Context, path, cache, reason string, stepErr error) error {
	started := time.Now()
	info, statErr := os.Stat(path)
	if stepErr == nil {
		stepErr = statErr
	}
	size := func() int64 {
		if info != nil {
			return info.Size()
		}
		return 0
	}()
	RecordStep(ctx, Step{Name: "build.shared_artifact", StartedAt: started, Duration: time.Since(started),
		Cache: cache, Reason: reason, OK: stepErr == nil, ExecutableBytes: size})
	return stepErr
}

type sharedBinaryCacheEntry struct {
	path    string
	modTime time.Time
	size    int64
}

func pruneSharedBinaries(root, keep string) error {
	artifactsRoot := filepath.Join(root, "artifacts")
	entries, err := os.ReadDir(artifactsRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var cached []sharedBinaryCacheEntry
	var total int64
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || len(entry.Name()) != 64 {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		binary, err := os.Stat(filepath.Join(artifactsRoot, entry.Name(), "application"))
		if err != nil {
			continue
		}
		cached = append(cached, sharedBinaryCacheEntry{path: entry.Name(), modTime: info.ModTime(), size: binary.Size()})
		total += binary.Size()
	}
	sort.Slice(cached, func(i, j int) bool { return cached[i].modTime.Before(cached[j].modTime) })
	for len(cached) > sharedBinaryCacheEntries || total > sharedBinaryCacheBytes {
		entry := cached[0]
		cached = cached[1:]
		if entry.path == keep {
			cached = append(cached, entry)
			if len(cached) == 1 {
				break
			}
			continue
		}
		if err := os.RemoveAll(filepath.Join(artifactsRoot, entry.path)); err != nil {
			return err
		}
		total -= entry.size
	}
	return nil
}
