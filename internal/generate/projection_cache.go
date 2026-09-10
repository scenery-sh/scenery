package generate

import (
	"crypto/sha256"
	"encoding/json"
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

func cachedProjection(result *Result, kind string, extra any, render func() ([]generatedFile, error)) ([]generatedFile, error) {
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
	input, err := json.Marshal([]any{kind, result.Root, machine.RuntimeProducer(), result.Manifest, result.FrameworkResources, sources, result.HTTPSurfaceRevisions, result.OpenAPIRevisions, extra})
	if err != nil {
		// Non-serializable synthetic input must not change rendering semantics.
		return render()
	}
	key := sha256.Sum256(input)
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
