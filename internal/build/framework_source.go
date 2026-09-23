package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/mod/modfile"

	"scenery.sh/internal/app"
)

// The producer builder stamps the content it actually compiles. A VCS commit
// alone cannot identify dirty source, and a running CLI must not rediscover a
// different source tree after another task edits its original checkout.
var linkedFrameworkDigest string

type FrameworkSource struct {
	Root   string              `json:"root"`
	Digest string              `json:"digest"`
	Inputs *BuildInputManifest `json:"build_input_manifest"`
}

func FrameworkSourceManifest(root string) (FrameworkSource, error) {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return FrameworkSource{}, err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return FrameworkSource{}, err
	}
	module, err := os.ReadFile(filepath.Join(canonical, "go.mod"))
	if err != nil {
		return FrameworkSource{}, err
	}
	if modfile.ModulePath(module) != "scenery.sh" {
		return FrameworkSource{}, fmt.Errorf("framework source is not the scenery.sh module: %s", canonical)
	}
	files, _, err := frameworkFingerprintFiles(canonical, nil)
	if err != nil {
		return FrameworkSource{}, err
	}
	entries := make(map[string]string, len(files))
	for _, relative := range files {
		if err := addBuildInput(entries, "framework/source/"+relative, filepath.Join(canonical, filepath.FromSlash(relative))); err != nil {
			return FrameworkSource{}, err
		}
	}
	manifest := newBuildInputManifest("scenery-producer", entries)
	// Source content identity is independent of the materializer's own version
	// and timestamp. The manifest retains that materializer as provenance.
	hash := sha256.New()
	_, _ = io.WriteString(hash, "scenery.framework-source\x00")
	for _, input := range manifest.Entries {
		_, _ = io.WriteString(hash, input.Identity+"\x00"+input.Digest+"\x00")
	}
	return FrameworkSource{Root: canonical, Digest: "sha256:" + hex.EncodeToString(hash.Sum(nil)), Inputs: manifest}, nil
}

func FrameworkProducerLinkerFlags(sourceDigest string) (string, error) {
	if !validFrameworkDigest(sourceDigest) {
		return "", fmt.Errorf("framework source digest is invalid")
	}
	return "-X=scenery.sh/internal/build.linkedFrameworkDigest=" + sourceDigest, nil
}

func validFrameworkDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && strings.ToLower(value) == value
}

// VerifyFrameworkProducer checks the build-time stamp against the actual
// selected source. It intentionally re-reads content at a lifecycle boundary.
func VerifyFrameworkProducer() (FrameworkSource, error) {
	if linkedFrameworkDigest == "" {
		return FrameworkSource{}, fmt.Errorf("scenery CLI has no content-bound framework producer; run scenery framework use to prepare a matching local executable, or build the repository through scripts/verify")
	}
	source, err := FrameworkSourceManifest(app.RepoRoot())
	if err != nil {
		return FrameworkSource{}, err
	}
	if source.Digest != linkedFrameworkDigest {
		return FrameworkSource{}, fmt.Errorf("scenery source changed after this CLI was built; the current runtime is retained; prepare a new framework snapshot and restart with its matching executable")
	}
	return source, nil
}

var executableDigest = sync.OnceValues(func() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	return digestExecutable(path)
})

func digestExecutable(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
