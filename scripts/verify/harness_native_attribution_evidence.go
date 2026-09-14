package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const nativeAttributionFileLimit int64 = 32 << 20
const nativeAttributionRunLimit int64 = 2 << 30

func nativeAttributionRead(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, nativeAttributionFileLimit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > nativeAttributionFileLimit {
		return nil, fmt.Errorf("attribution evidence exceeds 32 MiB: %s", path)
	}
	return data, nil
}

func nativeAttributionEvidenceSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > nativeAttributionFileLimit {
			return fmt.Errorf("invalid or oversized attribution evidence: %s", path)
		}
		total += info.Size()
		if total > nativeAttributionRunLimit {
			return fmt.Errorf("attribution evidence exceeds 2 GiB run limit")
		}
		return nil
	})
	return total, err
}

// Only inspect the application actually found in the ancestry. A successful
// policy query with no row means not-listed, not permission granted or denied.
func (b *nativeReloadBenchmark) attributionLauncherPolicy(ancestry []map[string]any) {
	for _, process := range ancestry {
		text, _ := process["observation"].(string)
		fields := strings.Fields(text)
		if len(fields) < 2 {
			continue
		}
		executable := strings.Join(fields[1:], " ")
		end := strings.Index(executable, ".app/")
		if end < 0 {
			continue
		}
		app := executable[:end+4]
		policy := map[string]any{"developer_tools": "unknown", "application": app,
			"ancestry_candidate_only": true, "reason": "responsible application attribution is not established by ancestry alone"}
		b.summary["launcher_policy"] = policy
		info := filepath.Join(app, "Contents", "Info.plist")
		for _, key := range []string{"CFBundleIdentifier", "CFBundleShortVersionString", "CFBundleVersion"} {
			data, err := b.command(b.appRoot, "launcher-"+key, "/usr/libexec/PlistBuddy", "-c", "Print :"+key, info)
			if err == nil {
				policy[key] = strings.TrimSpace(string(data))
			}
		}
		if digest, err := nativeReloadFileDigest(executable); err == nil {
			policy["executable_digest"] = digest
		}
		if data, err := b.command(b.appRoot, "launcher-signature", "codesign", "-dvv", app); err == nil {
			policy["signature"] = strings.TrimSpace(string(data))
		}
		identifier, _ := policy["CFBundleIdentifier"].(string)
		home, err := os.UserHomeDir()
		if err != nil || identifier == "" {
			return
		}
		query := "SELECT client,auth_value,auth_reason,last_modified FROM access WHERE service='kTCCServiceDeveloperTool' AND client='" + strings.ReplaceAll(identifier, "'", "''") + "';"
		var observations []map[string]any
		for i, database := range []string{filepath.Join(home, "Library/Application Support/com.apple.TCC/TCC.db"), "/Library/Application Support/com.apple.TCC/TCC.db"} {
			data, err := b.command(b.appRoot, fmt.Sprintf("launcher-policy-%d", i), "sqlite3", "-readonly", "-json", database, query)
			observation := map[string]any{"database": database, "read_succeeded": err == nil, "output": strings.TrimSpace(string(data))}
			if err != nil {
				observation["error"] = err.Error()
			}
			observations = append(observations, observation)
		}
		policy["read_only_policy_observations"] = observations
		return
	}
}
