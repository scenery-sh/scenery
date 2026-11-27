package parse

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sort"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/gotarget"
)

// AnalysisInputDigest names everything an analysis of target with overlay
// consumes, so that two analyses with one digest have one result, in this
// process or another: the target with its flags and patterns, the identity of
// the Go tool its environment selects, the module files, local replacements,
// every source directory of the module and the overlay's bytes. It fails
// when those inputs cannot be named completely, such as a vendored module or
// an environment that selects no go command.
func AnalysisInputDigest(root string, overlay map[string][]byte, target gotarget.Context) (string, error) {
	digests, err := AnalysisInputDigests(root, overlay, []gotarget.Context{target})
	if err != nil {
		return "", err
	}
	return digests[0], nil
}

// AnalysisInputDigests computes AnalysisInputDigest for each target, reusing
// overlay canonicalization and analysisSourceDirs across targets that share a
// canonical module root.
func AnalysisInputDigests(root string, overlay map[string][]byte, targets []gotarget.Context) ([]string, error) {
	if len(targets) == 0 {
		return nil, nil
	}
	logicalRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	physicalRoot, err := filepath.EvalSymlinks(logicalRoot)
	if err != nil {
		return nil, err
	}
	canonical := canonicalizeAnalysisOverlay(logicalRoot, physicalRoot, overlay)
	paths := make([]string, 0, len(canonical))
	for path := range canonical {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	overlayHash := sha256.New()
	_, _ = overlayHash.Write([]byte("overlay\x00"))
	for _, path := range paths {
		sum := sha256.Sum256(canonical[path])
		_, _ = overlayHash.Write([]byte(path + "\x00" + hex.EncodeToString(sum[:]) + "\x00"))
	}
	overlayDigest := overlayHash.Sum(nil)

	dirCache := map[string]map[string]string{}
	out := make([]string, len(targets))
	for i, target := range targets {
		if len(target.Patterns) == 0 {
			return nil, errors.New("go target has no package patterns")
		}
		moduleRoot, err := filepath.EvalSymlinks(target.ModuleRoot)
		if err != nil {
			return nil, err
		}
		tool, resolved := gotarget.ToolIdentity(gotarget.Hermetic(&target))
		if !resolved {
			return nil, errors.New("the target environment selects no go command")
		}
		flags := gotarget.WithTrimpath(append([]string(nil), target.BuildFlags...))
		if len(target.BuildTags) > 0 {
			flags = append(flags, "-tags="+strings.Join(target.BuildTags, ","))
		}
		linkedRoot, _, err := app.LinkedFrameworkReplaceRoot(moduleRoot)
		if err != nil {
			return nil, err
		}
		stamp, err := captureAnalysisStampCached(moduleRoot, &target, tool, flags, target.Patterns, linkedRoot, dirCache)
		if err != nil {
			return nil, err
		}
		hash := sha256.New()
		write := func(parts ...string) {
			for _, part := range parts {
				_, _ = hash.Write([]byte(part + "\x00"))
			}
		}
		write("scenery.analysis-input/1", physicalRoot, moduleRoot, stamp.identity, digestAnalysisDirs(stamp.dirs))
		_, _ = hash.Write(overlayDigest)
		out[i] = hex.EncodeToString(hash.Sum(nil))
	}
	return out, nil
}
