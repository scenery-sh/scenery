package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

// reusableStamp uses the filesystem change timestamp in addition to ordinary
// metadata. Platforms that do not expose one conservatively rehash: size and
// mtime alone are never content identity.
func reusableStamp(previous map[string]fileStamp, rel string, info fs.FileInfo, embedded bool) (fileStamp, bool) {
	prev, ok := previous[rel]
	changeTime := fileChangeTime(info)
	if !ok || changeTime == 0 || prev.changeTime != changeTime || prev.embed != embedded || prev.size != info.Size() || prev.mode != uint32(info.Mode().Perm()) || !prev.modTime.Equal(info.ModTime().UTC().Round(0)) {
		return fileStamp{}, false
	}
	return prev, true
}

func stampWatchedFile(path string, info fs.FileInfo, embedded bool) (fileStamp, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return fileStamp{}, nil, err
	}
	sum := sha256.Sum256(data)
	return fileStamp{
		modTime:    info.ModTime().UTC().Round(0),
		changeTime: fileChangeTime(info),
		size:       info.Size(),
		mode:       uint32(info.Mode().Perm()),
		hash:       hex.EncodeToString(sum[:]),
		embed:      embedded,
		data:       append([]byte(nil), data...),
	}, data, nil
}

func fileChangeTime(info fs.FileInfo) int64 {
	if info == nil || info.Sys() == nil {
		return 0
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 0
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return 0
	}
	for _, name := range []string{"Ctimespec", "Ctim", "Ctimen"} {
		stamp := value.FieldByName(name)
		if !stamp.IsValid() || stamp.Kind() != reflect.Struct {
			continue
		}
		seconds, nanos := stamp.FieldByName("Sec"), stamp.FieldByName("Nsec")
		if seconds.IsValid() && nanos.IsValid() && seconds.CanInt() && nanos.CanInt() {
			return seconds.Int()*int64(time.Second) + nanos.Int()
		}
	}
	return 0
}

func snapshotsEqual(a, b fileSnapshot) bool {
	if a.retryGenerated && len(changedGeneratedContent(a, b)) > 0 {
		return false
	}
	if len(a.generated) != len(b.generated) {
		return false
	}
	for path, present := range a.generated {
		if other, ok := b.generated[path]; !ok || other != present {
			return false
		}
	}
	return a.compilerValid == b.compilerValid && stampMapsEqual(a.files, b.files) && runtimeCompilerFilesEqual(a, b)
}

// buildInputSnapshotsEqual compares the complete authored/runtime input set.
// Generated presence is publication output, not authority to accept or reject
// a candidate prepared from the captured authored bytes.
func buildInputSnapshotsEqual(a, b fileSnapshot) bool {
	if a.retryGenerated && len(changedGeneratedContent(a, b)) > 0 {
		return false
	}
	return a.compilerValid == b.compilerValid && stampMapsEqual(a.files, b.files) && runtimeCompilerFilesEqual(a, b)
}

func runtimeCompilerFilesEqual(a, b fileSnapshot) bool {
	for path, stamp := range a.compilerFiles {
		if !compilerFileAffectsRuntime(a, b, path) {
			continue
		}
		if other, ok := b.compilerFiles[path]; !ok || !stamp.sameContent(other) {
			return false
		}
	}
	for path := range b.compilerFiles {
		if !compilerFileAffectsRuntime(a, b, path) {
			continue
		}
		if _, ok := a.compilerFiles[path]; !ok {
			return false
		}
	}
	for path := range a.compilerAbsent {
		if !compilerFileAffectsRuntime(a, b, path) {
			continue
		}
		if _, ok := b.compilerAbsent[path]; !ok {
			return false
		}
	}
	for path := range b.compilerAbsent {
		if !compilerFileAffectsRuntime(a, b, path) {
			continue
		}
		if _, ok := a.compilerAbsent[path]; !ok {
			return false
		}
	}
	return true
}

// Test sources remain part of the compiler's exact workspace revision so a
// later real rebuild can bind current identity. They do not independently
// schedule or invalidate the runtime unless runtime code explicitly embeds
// them, matching the public watch contract.
func compilerFileAffectsRuntime(a, b fileSnapshot, path string) bool {
	if !strings.HasSuffix(filepath.ToSlash(path), "_test.go") {
		return true
	}
	implementationA, knownA := compilerInputImplementation(a, path)
	implementationB, knownB := compilerInputImplementation(b, path)
	if (knownA && !implementationA) || (knownB && !implementationB) {
		return true
	}
	_, inA := a.files[path]
	_, inB := b.files[path]
	return inA || inB
}

func compilerInputImplementation(snapshot fileSnapshot, path string) (bool, bool) {
	if implementation, ok := snapshot.compilerImpl[path]; ok {
		return implementation, true
	}
	implementation, ok := snapshot.compilerAbsent[path]
	return implementation, ok
}

func stampMapsEqual(a, b map[string]fileStamp) bool {
	if len(a) != len(b) {
		return false
	}
	for path, stamp := range a {
		if other, ok := b[path]; !ok || !stamp.sameContent(other) {
			return false
		}
	}
	return true
}

func changedPaths(before, after fileSnapshot) []string {
	seen := make(map[string]bool, len(before.files)+len(after.files)+len(before.compilerFiles)+len(after.compilerFiles)+len(before.compilerAbsent)+len(after.compilerAbsent))
	paths := make([]string, 0, len(seen))
	for path, stamp := range before.files {
		seen[path] = true
		if other, ok := after.files[path]; !ok || !stamp.sameContent(other) {
			paths = append(paths, path)
		}
	}
	for path := range after.files {
		if _, ok := seen[path]; ok {
			continue
		}
		paths = append(paths, path)
		seen[path] = true
	}
	for path, stamp := range before.compilerFiles {
		if seen[path] {
			continue
		}
		if !compilerFileAffectsRuntime(before, after, path) {
			continue
		}
		seen[path] = true
		if other, ok := after.compilerFiles[path]; !ok || !stamp.sameContent(other) {
			paths = append(paths, path)
		}
	}
	for path := range after.compilerFiles {
		if !seen[path] && compilerFileAffectsRuntime(before, after, path) {
			paths = append(paths, path)
			seen[path] = true
		}
	}
	for path := range before.compilerAbsent {
		if seen[path] || !compilerFileAffectsRuntime(before, after, path) {
			continue
		}
		seen[path] = true
		if _, ok := after.compilerAbsent[path]; !ok {
			paths = append(paths, path)
		}
	}
	for path := range after.compilerAbsent {
		if !seen[path] && compilerFileAffectsRuntime(before, after, path) {
			paths = append(paths, path)
			seen[path] = true
		}
	}
	for path, present := range before.generated {
		if other, ok := after.generated[path]; (!ok || other != present) && !seen[path] {
			paths = append(paths, path)
			seen[path] = true
		}
	}
	for path := range after.generated {
		if _, existed := before.generated[path]; !existed && !seen[path] {
			paths = append(paths, path)
		}
	}
	if before.retryGenerated {
		for _, path := range changedGeneratedContent(before, after) {
			if !seen[path] {
				paths = append(paths, path)
			}
		}
	}
	sort.Strings(paths)
	return paths
}
