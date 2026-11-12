package parse

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sort"
	"strings"

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
	if len(target.Patterns) == 0 {
		return "", errors.New("go target has no package patterns")
	}
	logicalRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	physicalRoot, err := filepath.EvalSymlinks(logicalRoot)
	if err != nil {
		return "", err
	}
	moduleRoot, err := filepath.EvalSymlinks(target.ModuleRoot)
	if err != nil {
		return "", err
	}
	tool, resolved := gotarget.ToolIdentity(gotarget.Hermetic(&target))
	if !resolved {
		return "", errors.New("the target environment selects no go command")
	}
	// These are the flags analyze passes, without the private module file it
	// creates for an overlay: that file is a copy of go.mod, which the stamp
	// reads.
	flags := gotarget.WithTrimpath(append([]string(nil), target.BuildFlags...))
	if len(target.BuildTags) > 0 {
		flags = append(flags, "-tags="+strings.Join(target.BuildTags, ","))
	}
	stamp, err := captureAnalysisStamp(moduleRoot, &target, tool, flags, target.Patterns)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	write := func(parts ...string) {
		for _, part := range parts {
			_, _ = hash.Write([]byte(part + "\x00"))
		}
	}
	write("scenery.analysis-input/1", physicalRoot, moduleRoot, stamp.identity, digestAnalysisDirs(stamp.dirs))
	canonical := canonicalizeAnalysisOverlay(logicalRoot, physicalRoot, overlay)
	paths := make([]string, 0, len(canonical))
	for path := range canonical {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	write("overlay")
	for _, path := range paths {
		sum := sha256.Sum256(canonical[path])
		write(path, hex.EncodeToString(sum[:]))
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
