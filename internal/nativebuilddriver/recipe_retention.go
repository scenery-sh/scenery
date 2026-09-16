package nativebuilddriver

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Retained state is what makes a recipe reusable: the archives its compiles
// produced, the support inputs its tools read, and the source snapshots that
// identify what was compiled. Every retained path is content addressed under
// one state root, so recipes of one workspace share what their closures have
// in common, and every path is validated against its recorded identity before
// a build reuses it.

// retainBootstrapState moves recorded state out of the recording directory and
// into the shared store. Archives are named by content, so a second entrypoint
// of the same workspace adopts the archives the first one already retained.
func (recipe *Recipe) retainBootstrapState(stateRoot string) error {
	retained := make(map[string]RetainedFile, len(recipe.Retained))
	var retainedBytes int64
	for original, recorded := range recipe.ArchiveByOld {
		file := recipe.Retained[recorded]
		if file.Digest == "" {
			return fmt.Errorf("recorded archive is unaccounted: %s", original)
		}
		target := filepath.Join(stateRoot, "artifacts", strings.TrimPrefix(file.Digest, "sha256:")+".a")
		adopted, err := adoptRetainedFile(recorded, target, file)
		if err != nil {
			return fmt.Errorf("retain recorded archive %s: %w", original, err)
		}
		recipe.ArchiveByOld[original] = target
		if _, counted := retained[target]; !counted {
			retained[target] = adopted
			retainedBytes += adopted.Bytes
		}
	}
	recipe.Retained, recipe.RetainedBytes = retained, retainedBytes
	recipe.RetentionLimit = retainedBytes*2 + 512<<20
	for original, path := range recipe.Current.SnapshotFiles {
		digest := recipe.Current.Files[original]
		if path == "" || digest == "" {
			continue
		}
		target := filepath.Join(stateRoot, "snapshots", digestName(original+"\x00"+digest)+filepath.Ext(original))
		if samePath(path, target) {
			continue
		}
		// A snapshot is copied, never moved: a capture may name the workspace
		// file itself, which belongs to the developer, not to retained state.
		copied, err := CopyRegular(path, target)
		if err != nil {
			return fmt.Errorf("retain source snapshot %s: %w", original, err)
		}
		if copied.Digest != digest {
			return fmt.Errorf("source snapshot identity changed: %s", original)
		}
		recipe.Current.SnapshotFiles[original] = target
	}
	recipe.Bootstrap.SnapshotFiles = cloneStrings(recipe.Current.SnapshotFiles)
	if err := recipe.materializeSupport(stateRoot); err != nil {
		return err
	}
	return recipe.rebuildSupportAccounting()
}

// adoptRetainedFile links one recorded archive into its content-addressed path
// and describes what the store now holds. A content-addressed name and an equal
// size do not prove an existing entry's content, so an entry that already
// exists is hashed once here, where it is adopted; the stamp returned with its
// digest is what later reuse trusts. Captures of sibling entrypoints run
// concurrently: an entry with the expected content is adopted as it is, so an
// archive another recipe identifies by its recorded stamp is never replaced,
// and an entry whose content differs is atomically replaced by the recorded
// archive. The recorded copy is left to its owner, which discards the whole
// recording directory.
func adoptRetainedFile(recorded, target string, file RetainedFile) (RetainedFile, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return RetainedFile{}, err
	}
	switch err := os.Link(recorded, target); {
	case err == nil:
	case errors.Is(err, os.ErrExist):
		digest, _, digestErr := FileDigest(target)
		if digestErr != nil || digest != file.Digest {
			if err := replaceRetainedFile(recorded, target, file); err != nil {
				return RetainedFile{}, err
			}
		}
	default:
		// A recording directory on another filesystem cannot be linked from.
		copied, copyErr := CopyRegular(recorded, target)
		if copyErr != nil {
			return RetainedFile{}, copyErr
		}
		if copied.Digest != file.Digest {
			return RetainedFile{}, fmt.Errorf("recorded file identity changed: %s", recorded)
		}
	}
	info, statErr := os.Lstat(target)
	if statErr != nil {
		return RetainedFile{}, statErr
	}
	if !info.Mode().IsRegular() {
		return RetainedFile{}, fmt.Errorf("retained state is not a regular file: %s", target)
	}
	if file.Bytes != 0 && info.Size() != file.Bytes {
		return RetainedFile{}, fmt.Errorf("retained state differs in size: %s", target)
	}
	return RetainedFile{Digest: file.Digest, Bytes: info.Size(), Stamp: fileStamp(info)}, nil
}

// replaceRetainedFile atomically replaces a content-addressed entry whose
// content is not its name with the recorded file, which must hold that content.
func replaceRetainedFile(recorded, target string, file RetainedFile) error {
	temporary, err := os.CreateTemp(filepath.Dir(target), ".replace-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	_ = temporary.Close()
	_ = os.Remove(name)
	defer func() { _ = os.Remove(name) }()
	copied, err := CopyRegular(recorded, name)
	if err != nil {
		return err
	}
	if copied.Digest != file.Digest {
		return fmt.Errorf("recorded file identity changed: %s", recorded)
	}
	return os.Rename(name, target)
}

func (recipe *Recipe) materializeSupport(stateRoot string) error {
	for importPath, source := range recipe.Compiles {
		action := *source
		action.Argv = append([]string(nil), source.Argv...)
		action.Files = cloneFileCopies(source.Files)
		for index, file := range action.Files {
			if _, isSource := recipe.Current.Files[file.Original]; isSource {
				continue
			}
			retained, err := retainSupportCopy(file, stateRoot)
			if err != nil {
				return err
			}
			action.Files[index] = retained
		}
		recipe.Compiles[importPath] = &action
	}
	link := *recipe.Link
	link.Argv = append([]string(nil), recipe.Link.Argv...)
	link.Files = cloneFileCopies(recipe.Link.Files)
	for index, file := range link.Files {
		if _, isSource := recipe.Current.Files[file.Original]; isSource {
			continue
		}
		retained, err := retainSupportCopy(file, stateRoot)
		if err != nil {
			return err
		}
		link.Files[index] = retained
	}
	recipe.Link = &link
	for original, snapshot := range recipe.Current.SnapshotFiles {
		if snapshot == "" || recipe.Current.Files[original] == "" {
			continue
		}
		if retained, ok := recipe.Support[snapshot]; ok && retained.Digest == recipe.Current.Files[original] {
			continue
		}
		target := filepath.Join(stateRoot, "snapshots", digestName(original+"\x00"+recipe.Current.Files[original])+filepath.Ext(original))
		if !samePath(snapshot, target) {
			copy, err := CopyRegular(snapshot, target)
			if err != nil {
				return err
			}
			if copy.Digest != recipe.Current.Files[original] {
				return fmt.Errorf("refreshed source snapshot identity changed: %s", original)
			}
		}
		recipe.Current.SnapshotFiles[original] = target
	}
	return nil
}

func retainSupportCopy(file FileCopy, stateRoot string) (FileCopy, error) {
	extension := filepath.Ext(file.Original)
	target := filepath.Join(stateRoot, "support", strings.TrimPrefix(file.Digest, "sha256:")+extension)
	if samePath(file.Copy, target) {
		return file, nil
	}
	copy, err := CopyRegular(file.Copy, target)
	if err != nil {
		return FileCopy{}, err
	}
	if copy.Digest != file.Digest {
		return FileCopy{}, fmt.Errorf("support input identity changed: %s", file.Original)
	}
	copy.Original = file.Original
	return copy, nil
}

func (recipe *Recipe) retainArchiveMapping(original, stateRoot string) error {
	digest, _, err := FileDigest(original)
	if err != nil {
		return fmt.Errorf("retain graph-refresh archive %s: %w", original, err)
	}
	target := filepath.Join(stateRoot, "artifacts", strings.TrimPrefix(digest, "sha256:")+".a")
	copy, err := CopyRegular(original, target)
	if err != nil {
		return err
	}
	if copy.Digest != digest {
		return fmt.Errorf("graph-refresh archive identity changed: %s", original)
	}
	recipe.ArchiveByOld[original] = target
	return nil
}

func (recipe *Recipe) validateRetainedArtifacts() error {
	return validateRetainedFiles(recipe.Retained)
}

func (recipe *Recipe) validateSupportArtifacts() error {
	return validateRetainedFiles(recipe.Support)
}

// ValidateRetainedState reports the live archive and support validation costs
// for benchmark controls that keep stock cmd/go as the executor.
func (recipe *Recipe) ValidateRetainedState() (archiveMS, supportMS float64, err error) {
	if err := recipe.Validate(); err != nil {
		return 0, 0, err
	}
	started := time.Now()
	if err := recipe.validateRetainedArtifacts(); err != nil {
		return elapsedMS(started), 0, err
	}
	archiveMS = elapsedMS(started)
	started = time.Now()
	err = recipe.validateSupportArtifacts()
	supportMS = elapsedMS(started)
	return archiveMS, supportMS, err
}

func validateRetainedFiles(files map[string]RetainedFile) error {
	for path, expected := range files {
		if err := validateRetainedFile(path, expected); err != nil {
			return err
		}
	}
	return nil
}

func validateRetainedFile(path string, expected RetainedFile) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("retained build input is unavailable: %s", path)
	}
	stamp := fileStamp(info)
	if stamp == expected.Stamp {
		return nil
	}
	digest, size, err := FileDigest(path)
	if err != nil || digest != expected.Digest || size != expected.Bytes {
		return fmt.Errorf("retained build input identity changed: %s", path)
	}
	return nil
}

func retainSupportFile(recipe *Recipe, file FileCopy) error {
	return retainSupportPath(recipe, file.Copy, file.Digest)
}

func retainSupportPath(recipe *Recipe, path, expectedDigest string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	digest, size, err := FileDigest(path)
	if err != nil {
		return err
	}
	if expectedDigest != "" && digest != expectedDigest {
		return fmt.Errorf("retained support digest differs: %s", path)
	}
	recipe.Support[path] = RetainedFile{Digest: digest, Bytes: size, Stamp: fileStamp(info)}
	return nil
}

func retainActionSupport(recipe *Recipe, files map[int]FileCopy, sourceFiles map[string]string) error {
	for _, file := range files {
		if _, source := sourceFiles[file.Original]; source {
			continue
		}
		if err := retainSupportFile(recipe, file); err != nil {
			return err
		}
	}
	return nil
}

func retainToolDigest(recipe *Recipe, tool string) error {
	return retainToolDigestWith(recipe, tool, FileDigest)
}

func retainToolDigestWith(recipe *Recipe, tool string, digestFile func(string) (string, int64, error)) error {
	path := tool
	if !filepath.IsAbs(path) {
		var err error
		path, err = exec.LookPath(path)
		if err != nil {
			return err
		}
	}
	if recipe.ToolDigests[path] != "" {
		return nil
	}
	digest, _, err := digestFile(path)
	if err != nil {
		return err
	}
	recipe.ToolDigests[path] = digest
	return nil
}
