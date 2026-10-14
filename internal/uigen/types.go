// Package uigen renders the optional UI projection of the canonical graph.
// Its caller owns verification, generated-file ownership and publication.
package uigen

import (
	"regexp"

	"scenery.sh/internal/graph"
)

type Resource = graph.Resource
type Manifest = graph.Manifest
type Origin = graph.Origin

// Input is the validated graph and workspace location needed by UI rendering.
// Rendering does not compile declarations or mutate the graph.
type Input struct {
	Root     string
	Manifest *graph.Manifest
}

// File is a rendered artifact in memory. The parent generator decides when and
// where to publish it together with the rest of the artifact set.
type File struct {
	Path  string
	Bytes []byte
}

var httpPathParameterPattern = regexp.MustCompile(`\{([a-z][a-z0-9_]*)\}`)

func cloneProjection(files []File) []File {
	cloned := make([]File, len(files))
	for i, file := range files {
		cloned[i] = file
		cloned[i].Bytes = append([]byte(nil), file.Bytes...)
	}
	return cloned
}
