package generate

import (
	"crypto/sha256"
	"encoding/json"
	"sort"
	"sync"

	"scenery.sh/internal/machine"
	"scenery.sh/internal/scn"
)

// This process-local cache holds only renderer-owned bytes. Filesystem checks,
// implementation analysis and transactional publication remain with callers.
// Its lifetime also prevents reuse across different executable producers.
var projections = struct {
	sync.Mutex
	entries map[[32]byte][]generatedFile
	bytes   int
}{entries: make(map[[32]byte][]generatedFile)}

const projectionCacheLimit = 32 << 20

// One render operation may select several projections from the same immutable
// result. Capture their complete common input once, never across operations.
type projectionInput struct {
	digest [32]byte
	err    error
}

func newProjectionInput(result *Result) projectionInput {
	return sharedProjectionInput(result)
}

func canUseFastProjectionInput(result *Result) bool {
	if result == nil || !result.Valid() || result.Manifest == nil || len(result.Sources) == 0 {
		return false
	}
	if !isCanonicalSHA256Digest(result.Manifest.ContractRevision) || !isCanonicalSHA256Digest(result.WorkspaceRevision) {
		return false
	}
	for _, s := range result.Sources {
		if s == nil || len(s.Bytes) == 0 {
			return false
		}
	}
	return true
}

func fastProjectionInputDigest(result *Result) ([32]byte, error) {
	type compactSource struct {
		ID, Path, Relative string
		External           bool
		BytesSHA256        [32]byte
	}
	sources := make([]compactSource, len(result.Sources))
	for i, s := range result.Sources {
		sources[i] = compactSource{
			ID: s.ID, Path: s.Path, Relative: s.Relative, External: s.External,
			BytesSHA256: sha256.Sum256(s.Bytes),
		}
	}
	var renderers []string
	for _, r := range result.Manifest.Resources {
		if r.Kind == "scenery.renderer" {
			digest, _ := r.Spec["implementation_digest"].(string)
			renderers = append(renderers, r.Address+"\x00"+digest)
		}
	}
	sort.Strings(renderers)
	input, err := json.Marshal([]any{
		result.Root,
		machine.RuntimeProducer(),
		result.Manifest.ContractRevision,
		result.Manifest.SpecRevision,
		result.Manifest.SchemaRevision,
		result.Manifest.Application.Name,
		result.WorkspaceRevision,
		result.FrameworkResources,
		result.HTTPSurfaceRevisions,
		result.OpenAPIRevisions,
		result.ImplementationRevisions,
		sources,
		renderers,
	})
	return sha256.Sum256(input), err
}

func computeProjectionInput(result *Result) projectionInput {
	if canUseFastProjectionInput(result) {
		digest, err := fastProjectionInputDigest(result)
		return projectionInput{digest: digest, err: err}
	}
	// Include parsed declarations as well as original bytes: callers can supply
	// synthetic compiler snapshots without reparsing a source file.
	type sourceInput struct {
		ID, Path, Relative string
		Bytes              []byte
		Blocks             []*scn.Block
		External           bool
	}
	sources := make([]sourceInput, len(result.Sources))
	for i, source := range result.Sources {
		if source != nil {
			sources[i] = sourceInput{source.ID, source.Path, source.Relative, source.Bytes, source.Blocks, source.External}
		}
	}
	input, err := json.Marshal([]any{result.Root, machine.RuntimeProducer(), result.Manifest, result.FrameworkResources, sources, result.HTTPSurfaceRevisions, result.OpenAPIRevisions})
	return projectionInput{digest: sha256.Sum256(input), err: err}
}

func (input projectionInput) key(kind string, extra any) ([32]byte, error) {
	if input.err != nil {
		return [32]byte{}, input.err
	}
	data, err := json.Marshal([]any{kind, input.digest, extra})
	return sha256.Sum256(data), err
}

func cachedProjection(input projectionInput, kind string, extra any, render func() ([]generatedFile, error)) ([]generatedFile, error) {
	key, err := input.key(kind, extra)
	if err != nil {
		// Non-serializable synthetic input must not change rendering semantics.
		return render()
	}
	projections.Lock()
	files, found := projections.entries[key]
	if found {
		files = cloneProjection(files)
	}
	projections.Unlock()
	if found {
		return files, nil
	}
	files, err = render()
	if err != nil {
		return nil, err
	}
	size := 0
	for _, file := range files {
		size += len(file.Path) + len(file.Bytes)
	}
	if size <= projectionCacheLimit {
		projections.Lock()
		if _, exists := projections.entries[key]; !exists {
			if projections.bytes+size > projectionCacheLimit || len(projections.entries) >= 64 {
				clear(projections.entries)
				projections.bytes = 0
			}
			projections.entries[key] = cloneProjection(files)
			projections.bytes += size
		}
		projections.Unlock()
	}
	return files, nil
}

func cloneProjection(files []generatedFile) []generatedFile {
	cloned := make([]generatedFile, len(files))
	for i, file := range files {
		cloned[i] = file
		cloned[i].Bytes = append([]byte(nil), file.Bytes...)
	}
	return cloned
}
