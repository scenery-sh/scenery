package deployplan

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"scenery.sh/internal/graph"
	"scenery.sh/internal/scn"
)

var rootResourceKinds = map[string]bool{
	"go_module": true, "go_toolchain": true, "go_target": true, "http_gateway": true,
	"authentication": true, "authorization": true, "workload_identity": true, "pipeline": true,
	"provider": true, "data_source": true, "execution_engine": true, "event_bus": true,
	"secret_store": true, "secret": true, "deployment": true, "typescript_client": true, "patch": true,
}

func resourcesByAddress(manifest *Manifest) map[string]Resource {
	result := map[string]Resource{}
	if manifest != nil {
		for _, resource := range manifest.Resources {
			result[resource.Address] = resource
		}
	}
	return result
}

func resolveResourceRef(resource Resource, reference, kind string) string {
	if reference == "" || strings.Contains(reference, "/") {
		return reference
	}
	parts := strings.Split(reference, ".")
	if len(parts) != 2 {
		return reference
	}
	module := resource.Module
	if rootResourceKinds[kind] || rootResourceKinds[parts[0]] {
		module = "app"
	}
	return graph.ResourceAddress(module, parts[0], parts[1])
}

func refString(value any) string {
	if object, ok := value.(map[string]any); ok {
		text, _ := object["$ref"].(string)
		return text
	}
	text, _ := value.(string)
	return text
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if scalar, ok := value.(map[string]any); ok {
		text, _ := scalar["value"].(string)
		return text
	}
	return ""
}

func canonicalStrings(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		if value != "" {
			set[value] = true
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func cloneMapValue(value any) map[string]any {
	result := map[string]any{}
	if source, ok := value.(map[string]any); ok {
		for key, item := range source {
			result[key] = item
		}
	}
	return result
}

func hasErrors(diagnostics []Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return true
		}
	}
	return false
}

func firstError(diagnostics []Diagnostic) string {
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return diagnostic.Code + ": " + diagnostic.Message
		}
	}
	return "unknown error"
}

func revisionHash(prefix string, value any) string { return graph.RevisionHash(prefix, value) }
func isCanonicalSHA256Digest(value string) bool    { return graph.IsCanonicalSHA256Digest(value) }
func rejectPathSymlinks(root, target string) error { return scn.RejectPathSymlinks(root, target) }

func confinedPath(root, relative string) (string, error) {
	if filepath.IsAbs(relative) || strings.HasPrefix(filepath.Clean(relative), "..") {
		return "", fmt.Errorf("path escape")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target := filepath.Join(absoluteRoot, filepath.FromSlash(relative))
	if !scn.PathWithin(absoluteRoot, target) {
		return "", fmt.Errorf("path escape")
	}
	if err := scn.RejectPathSymlinks(absoluteRoot, filepath.Dir(target)); err != nil {
		return "", err
	}
	return target, nil
}

func atomicWriteSynced(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary := path + ".tmp"
	_ = os.Remove(temporary)
	if err := writeSyncedFile(temporary, data, mode); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func writeSyncedFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}
