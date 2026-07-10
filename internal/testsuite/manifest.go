package testsuite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"scenery.sh/internal/envpolicy"
)

const manifestSchemaVersion = "scenery.test-binary-cache.v1"

type listedPackage struct {
	Dir          string
	ImportPath   string
	BuildID      string
	ForTest      string
	TestGoFiles  []string
	XTestGoFiles []string
}

type testPackage struct {
	Dir        string `json:"dir"`
	ImportPath string `json:"import_path"`
	BuildID    string `json:"build_id"`
	Binary     string `json:"binary"`
}

type cacheManifest struct {
	SchemaVersion  string        `json:"schema_version"`
	Fingerprint    string        `json:"fingerprint"`
	Packages       []testPackage `json:"packages"`
	NoTestPackages []string      `json:"no_test_packages"`
}

func readManifest(path, fingerprint string, refresh bool) (cacheManifest, bool) {
	if refresh {
		return cacheManifest{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cacheManifest{}, false
	}
	var manifest cacheManifest
	if json.Unmarshal(data, &manifest) != nil || manifest.SchemaVersion != manifestSchemaVersion || manifest.Fingerprint != fingerprint {
		return cacheManifest{}, false
	}
	return manifest, true
}

func writeManifest(path string, manifest cacheManifest) error {
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	return writeAtomic(path, data, 0o644)
}

func listTestPackages(ctx context.Context, repoRoot, cacheDir, fingerprint string, env []string) (cacheManifest, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-test", "-export", "-json", "./...")
	configureCommandCancellation(cmd)
	cmd.Dir = repoRoot
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		return cacheManifest{}, fmt.Errorf("go list test packages: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout))
	manifest := cacheManifest{SchemaVersion: manifestSchemaVersion, Fingerprint: fingerprint}
	allPackages := map[string]listedPackage{}
	for {
		var pkg listedPackage
		err := decoder.Decode(&pkg)
		if err == io.EOF {
			break
		}
		if err != nil {
			return cacheManifest{}, err
		}
		if pkg.ForTest == "" && !strings.HasSuffix(pkg.ImportPath, ".test") && !strings.Contains(pkg.ImportPath, " [") {
			allPackages[pkg.ImportPath] = pkg
		}
		if !strings.HasSuffix(pkg.ImportPath, ".test") || pkg.BuildID == "" {
			continue
		}
		importPath := strings.TrimSuffix(pkg.ImportPath, ".test")
		sum := sha256.Sum256([]byte(pkg.ImportPath + "\x00" + pkg.BuildID))
		manifest.Packages = append(manifest.Packages, testPackage{
			Dir:        pkg.Dir,
			ImportPath: importPath,
			BuildID:    pkg.BuildID,
			Binary:     filepath.Join(cacheDir, hex.EncodeToString(sum[:16])+".test"),
		})
	}
	testPackages := make(map[string]bool, len(manifest.Packages))
	for _, pkg := range manifest.Packages {
		testPackages[pkg.ImportPath] = true
	}
	for importPath := range allPackages {
		if !testPackages[importPath] {
			manifest.NoTestPackages = append(manifest.NoTestPackages, importPath)
		}
	}
	sort.Slice(manifest.Packages, func(i, j int) bool { return manifest.Packages[i].ImportPath < manifest.Packages[j].ImportPath })
	sort.Strings(manifest.NoTestPackages)
	return manifest, nil
}

func workspaceFingerprint(ctx context.Context, repoRoot string) (string, error) {
	hash := sha256.New()
	for _, value := range []string{
		runtime.Version(), runtime.GOROOT(), runtime.GOOS, runtime.GOARCH,
		envpolicy.Get("CGO_ENABLED"), envpolicy.Get("CGO_CFLAGS"), envpolicy.Get("CGO_CPPFLAGS"), envpolicy.Get("CGO_CXXFLAGS"), envpolicy.Get("CGO_LDFLAGS"),
		envpolicy.Get("GOEXPERIMENT"), envpolicy.Get("GOFLAGS"), envpolicy.Get("GOTOOLCHAIN"), envpolicy.Get("CC"), envpolicy.Get("CXX"), envpolicy.Get("PKG_CONFIG"),
	} {
		_, _ = io.WriteString(hash, value+"\x00")
	}
	for _, args := range [][]string{{"rev-parse", "HEAD"}, {"diff", "--binary", "--no-ext-diff", "HEAD", "--", "."}} {
		cmd := exec.CommandContext(ctx, "git", args...)
		configureCommandCancellation(cmd)
		cmd.Dir = repoRoot
		output, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		hash.Write(output)
	}
	cmd := exec.CommandContext(ctx, "git", "ls-files", "--others", "--exclude-standard", "-z")
	configureCommandCancellation(cmd)
	cmd.Dir = repoRoot
	untracked, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("list untracked repository inputs: %w", err)
	}
	for _, rawPath := range bytes.Split(untracked, []byte{0}) {
		if len(rawPath) == 0 {
			continue
		}
		hash.Write(rawPath)
		data, err := os.ReadFile(filepath.Join(repoRoot, string(rawPath)))
		if err != nil {
			return "", err
		}
		hash.Write(data)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func pruneUnreferencedBinaries(cacheDir string, packages []testPackage) {
	keep := make(map[string]bool, len(packages))
	for _, pkg := range packages {
		keep[filepath.Clean(pkg.Binary)] = true
	}
	matches, _ := filepath.Glob(filepath.Join(cacheDir, "*.test"))
	for _, path := range matches {
		if !keep[filepath.Clean(path)] {
			_ = os.Remove(path)
		}
	}
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".testsuite-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
