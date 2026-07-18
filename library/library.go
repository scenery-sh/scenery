// Package library loads and hot-swaps Scenery c-shared library artifacts.
//
// Generated scenerylib facades own typed operation APIs. This package owns the
// small untyped runtime below them: manifest validation, artifact digest and
// ABI checks, dlopen symbol binding, call accounting, and load-alongside swaps.
package library

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	ManifestKind           = "scenery.library.artifact"
	ManifestSchemaRevision = "sha256:35c46728a5d3ee20081c577ad1381f7b3821bb242a6bee0d61372c15af17dd66"
)

type Manifest struct {
	Kind           string              `json:"kind"`
	SchemaRevision string              `json:"schema_revision"`
	Library        string              `json:"library"`
	Version        string              `json:"version"`
	ABIHash        string              `json:"abi_hash"`
	Artifacts      map[string]Artifact `json:"artifacts"`
}

type Artifact struct {
	GOOS       string `json:"goos"`
	GOARCH     string `json:"goarch"`
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	GoVersion  string `json:"go_version"`
	GlibcFloor string `json:"glibc_floor,omitempty"`
}

type VersionInfo struct {
	Version  string `json:"version"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Active   int64  `json:"active"`
	Current  bool   `json:"current"`
	Draining bool   `json:"draining"`
}

type operationFunc func(unsafe.Pointer, uintptr, *unsafe.Pointer, *uintptr) int32
type freeFunc func(unsafe.Pointer)

type loadedVersion struct {
	version    string
	path       string
	sha256     string
	handle     uintptr
	free       freeFunc
	operations map[string]operationFunc
	active     atomic.Int64
	draining   atomic.Bool
}

// Loader owns all versions loaded for one declared library. Versions are
// intentionally retained for the life of the process: a Go c-shared runtime
// cannot be safely unloaded.
type Loader struct {
	name       string
	abiHash    string
	operations []string
	current    atomic.Pointer[loadedVersion]
	mu         sync.Mutex
	loaded     []*loadedVersion
}

func NewLoader(name, abiHash string, operationSymbols []string) (*Loader, error) {
	name, abiHash = strings.TrimSpace(name), strings.TrimSpace(abiHash)
	if name == "" || abiHash == "" {
		return nil, errors.New("library name and ABI hash are required")
	}
	operations := append([]string(nil), operationSymbols...)
	sort.Strings(operations)
	for index, symbol := range operations {
		if strings.TrimSpace(symbol) == "" || index > 0 && symbol == operations[index-1] {
			return nil, fmt.Errorf("library operation symbols must be unique and non-empty")
		}
	}
	return &Loader{name: name, abiHash: abiHash, operations: operations}, nil
}

func (l *Loader) Swap(manifestPath string) error {
	manifest, artifact, artifactPath, err := readManifestArtifact(manifestPath)
	if err != nil {
		return err
	}
	if manifest.Library != l.name {
		return fmt.Errorf("library manifest names %q, want %q", manifest.Library, l.name)
	}
	if manifest.ABIHash != l.abiHash {
		return fmt.Errorf("library manifest ABI hash %q does not match %q", manifest.ABIHash, l.abiHash)
	}
	digest, err := fileSHA256(artifactPath)
	if err != nil {
		return fmt.Errorf("hash library artifact: %w", err)
	}
	if digest != artifact.SHA256 {
		return fmt.Errorf("library artifact digest %q does not match manifest %q", digest, artifact.SHA256)
	}

	handle, err := purego.Dlopen(artifactPath, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return fmt.Errorf("load library artifact: %w", err)
	}
	// Do not call Dlclose after this point, including on validation failure.
	// Dlopen has started a Go runtime whose scheduler threads may outlive it.
	version := &loadedVersion{
		version: manifest.Version, path: artifactPath, sha256: digest, handle: handle,
		operations: make(map[string]operationFunc, len(l.operations)),
	}
	if err := bindLibFunc(&version.free, handle, "SceneryLibFree"); err != nil {
		return err
	}
	metadata := func(symbol string) (string, error) {
		var call operationFunc
		if err := bindLibFunc(&call, handle, symbol); err != nil {
			return "", err
		}
		return invoke(call, version.free, nil)
	}
	loadedABI, err := metadata("SceneryLibABIHash")
	if err != nil {
		return fmt.Errorf("read loaded library ABI: %w", err)
	}
	if loadedABI != l.abiHash {
		return fmt.Errorf("loaded library ABI hash %q does not match %q", loadedABI, l.abiHash)
	}
	loadedVersionText, err := metadata("SceneryLibVersion")
	if err != nil {
		return fmt.Errorf("read loaded library version: %w", err)
	}
	if loadedVersionText != manifest.Version {
		return fmt.Errorf("loaded library version %q does not match manifest %q", loadedVersionText, manifest.Version)
	}
	for _, symbol := range l.operations {
		var call operationFunc
		if err := bindLibFunc(&call, handle, symbol); err != nil {
			return err
		}
		version.operations[symbol] = call
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if previous := l.current.Swap(version); previous != nil {
		previous.draining.Store(true)
	}
	l.loaded = append(l.loaded, version)
	return nil
}

func bindLibFunc(target any, handle uintptr, symbol string) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("bind library symbol %s: %v", symbol, recovered)
		}
	}()
	purego.RegisterLibFunc(target, handle, symbol)
	return nil
}

func (l *Loader) Call(symbol string, input []byte) ([]byte, error) {
	version := l.current.Load()
	if version == nil {
		return nil, fmt.Errorf("shared library %s has no loaded version", l.name)
	}
	call := version.operations[symbol]
	if call == nil {
		return nil, fmt.Errorf("shared library %s does not expose %s", l.name, symbol)
	}
	version.active.Add(1)
	defer version.active.Add(-1)
	output, err := invokeBytes(call, version.free, input)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", l.name, version.version, err)
	}
	return output, nil
}

func (l *Loader) Versions() []VersionInfo {
	current := l.current.Load()
	l.mu.Lock()
	defer l.mu.Unlock()
	result := make([]VersionInfo, 0, len(l.loaded))
	for _, version := range l.loaded {
		result = append(result, VersionInfo{
			Version: version.version, Path: version.path, SHA256: version.sha256,
			Active: version.active.Load(), Current: version == current, Draining: version.draining.Load(),
		})
	}
	return result
}

func readManifestArtifact(path string) (Manifest, Artifact, string, error) {
	var manifest Manifest
	file, err := os.Open(path)
	if err != nil {
		return manifest, Artifact{}, "", fmt.Errorf("open library manifest: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 4<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, Artifact{}, "", fmt.Errorf("decode library manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return manifest, Artifact{}, "", errors.New("library manifest must contain exactly one JSON value")
	}
	if manifest.Kind != ManifestKind || manifest.SchemaRevision != ManifestSchemaRevision || manifest.Library == "" || manifest.Version == "" || manifest.ABIHash == "" {
		return manifest, Artifact{}, "", errors.New("library manifest has incomplete identity")
	}
	key := runtime.GOOS + "_" + runtime.GOARCH
	artifact, ok := manifest.Artifacts[key]
	if !ok {
		return manifest, Artifact{}, "", fmt.Errorf("unsupported platform for shared linkage: %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if artifact.GOOS != runtime.GOOS || artifact.GOARCH != runtime.GOARCH || artifact.Path == "" || artifact.GoVersion == "" || !canonicalSHA256(artifact.SHA256) {
		return manifest, Artifact{}, "", fmt.Errorf("library artifact %s has invalid platform identity", key)
	}
	if artifact.GOOS == "linux" && artifact.GlibcFloor == "" || artifact.GOOS != "linux" && artifact.GlibcFloor != "" {
		return manifest, Artifact{}, "", fmt.Errorf("library artifact %s has invalid platform identity", key)
	}
	artifactPath := filepath.Clean(filepath.FromSlash(artifact.Path))
	if filepath.IsAbs(artifactPath) || artifactPath == ".." || strings.HasPrefix(artifactPath, ".."+string(filepath.Separator)) || filepath.ToSlash(artifactPath) != artifact.Path {
		return manifest, Artifact{}, "", fmt.Errorf("library artifact %s path must be a normalized relative path", key)
	}
	return manifest, artifact, filepath.Join(filepath.Dir(path), artifactPath), nil
}

func invoke(call operationFunc, free freeFunc, input []byte) (string, error) {
	output, err := invokeBytes(call, free, input)
	return string(output), err
}

func invokeBytes(call operationFunc, free freeFunc, input []byte) ([]byte, error) {
	var inputPointer unsafe.Pointer
	if len(input) > 0 {
		inputPointer = unsafe.Pointer(unsafe.SliceData(input))
	}
	var outputPointer unsafe.Pointer
	var outputLength uintptr
	status := call(inputPointer, uintptr(len(input)), &outputPointer, &outputLength)
	if outputPointer == nil && outputLength != 0 {
		return nil, errors.New("library returned a nil output pointer with non-zero length")
	}
	var output []byte
	if outputLength > 0 {
		output = bytes.Clone(unsafe.Slice((*byte)(outputPointer), outputLength))
	}
	if outputPointer != nil {
		free(outputPointer)
	}
	runtime.KeepAlive(input)
	if status != 0 {
		if len(output) == 0 {
			return nil, fmt.Errorf("library call failed with status %d", status)
		}
		return nil, errors.New(string(output))
	}
	return output, nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func canonicalSHA256(value string) bool {
	encoded, ok := strings.CutPrefix(value, "sha256:")
	if !ok || len(encoded) != sha256.Size*2 || strings.ToLower(encoded) != encoded {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil
}
