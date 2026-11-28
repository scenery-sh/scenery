package main

import (
	"context"
	json "encoding/json"
	os "os"
	"os/exec"
	filepath "path/filepath"
	"runtime"
	strings "strings"
	"syscall"
	"time"

	"scenery.sh/internal/envpolicy"
)

const darwinMaxTMPDIRBytes = 50

// scrubSceneryProbeEnv strips leaked SCENERY_* and DATABASE_URL variables (and
// Go workspace overrides) from base before applying explicit probe overrides,
// preserving SCENERY_EXECUTION_PURPOSE so lane telemetry stays attributed.
func scrubSceneryProbeEnv(base []string, overrides ...string) []string {
	env := make([]string, 0, len(base)+len(overrides)+1)
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if ok && isScrubbedProbeEnvKey(key) {
			continue
		}
		env = append(env, item)
	}
	env = envWithOverrides(env, overrides...)
	if normalized, changed := normalizeDarwinTMPDIRInEnv(env); changed {
		env = envWithOverrides(env, "TMPDIR="+normalized)
	}
	return env
}

func isScrubbedProbeEnvKey(key string) bool {
	if key == "SCENERY_EXECUTION_PURPOSE" {
		return false
	}
	return key == "DATABASE_URL" ||
		key == "GOWORK" ||
		key == "GOFLAGS" ||
		key == detachedDevChildEnv ||
		strings.HasPrefix(key, "SCENERY_")
}

func normalizeDarwinTMPDIR() (string, bool) {
	return normalizeDarwinTMPDIRWith(runtime.GOOS, envpolicy.Get("TMPDIR"), statDirGID, queryDarwinUserTempDir)
}

func normalizeDarwinTMPDIRInEnv(env []string) (string, bool) {
	current := ""
	for _, item := range env {
		if value, ok := strings.CutPrefix(item, "TMPDIR="); ok {
			current = value
		}
	}
	return normalizeDarwinTMPDIRWith(runtime.GOOS, current, statDirGID, queryDarwinUserTempDir)
}

func normalizeDarwinTMPDIRWith(goos, current string, statGID func(string) (uint32, bool), darwinTempDir func() (string, error)) (string, bool) {
	if goos != "darwin" {
		return "", false
	}
	cleanedCurrent := strings.TrimSpace(current)
	if cleanedCurrent != "" {
		cleanedCurrent = filepath.Clean(cleanedCurrent)
	}
	if !darwinTMPDIRNeedsNormalization(cleanedCurrent, statGID) {
		return "", false
	}
	candidate, err := darwinTempDir()
	if err != nil {
		return "", false
	}
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return "", false
	}
	candidate = filepath.Clean(candidate)
	if candidate == cleanedCurrent {
		return "", false
	}
	return candidate, true
}

func darwinTMPDIRNeedsNormalization(dir string, statGID func(string) (uint32, bool)) bool {
	if dir == "" || len(dir) > darwinMaxTMPDIRBytes {
		return true
	}
	gid, ok := statGID(dir)
	if !ok || gid == 0 {
		return true
	}
	return false
}

func statDirGID(path string) (uint32, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return 0, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return stat.Gid, true
}

func queryDarwinUserTempDir() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "getconf", "DARWIN_USER_TEMP_DIR").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func envWithOverrides(base []string, overrides ...string) []string {
	keys := make(map[string]struct{}, len(overrides))
	for _, item := range overrides {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			keys[key] = struct{}{}
		}
	}
	env := make([]string, 0, len(base)+len(overrides))
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			if _, replace := keys[key]; replace {
				continue
			}
		}
		env = append(env, item)
	}
	return append(env, overrides...)
}

func envWithoutKeys(base []string, keys ...string) []string {
	if len(keys) == 0 {
		return append([]string(nil), base...)
	}
	omit := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		omit[key] = struct{}{}
	}
	env := make([]string, 0, len(base))
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			if _, drop := omit[key]; drop {
				continue
			}
		}
		env = append(env, item)
	}
	return env
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readHarnessJSON[T any](path string) (T, error) {
	var out T
	data, err := os.ReadFile(path)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(data, &out)
	return out, err
}

func cleanAbsPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}
