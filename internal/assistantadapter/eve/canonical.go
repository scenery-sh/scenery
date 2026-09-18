package eve

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
)

// A provider build records the absolute root it was built in, and a random
// build identifier, in its generated server module. A prepared build that other
// directories reuse — the production capsule extracted anywhere and a
// development overlay restored from its cache — must not depend on either, so
// every reusable build is canonicalized once, when it is produced, to one
// location-independent form. The helper resolves everything that depends on
// its start at runtime: its files relative to its own module, and its gateway
// address through the dynamic Scenery connection.
const (
	// CanonicalRoot is the root every reusable build records.
	CanonicalRoot = "/scenery-assistant"
	// ServerModulePath is the build's generated server module, relative to its
	// root.
	ServerModulePath = ".output/server/index.mjs"
)

var buildPathPattern = regexp.MustCompile(`\.eve/builds/[A-Za-z0-9_-]+/`)

// CanonicalizeServerModule replaces the root a build was produced in and its
// random build identifier with their canonical forms.
func CanonicalizeServerModule(data []byte, root string) []byte {
	data = bytes.ReplaceAll(data, []byte(filepath.Clean(root)), []byte(CanonicalRoot))
	return buildPathPattern.ReplaceAll(data, []byte(".eve/builds/build/"))
}

// ValidateCanonicalServerModule accepts only the server module shape a reusable
// build may have: one embedded manifest that records the canonical root and
// resolves the assistant's Scenery connection at runtime. A static Scenery
// connection would carry the gateway address of the build, which no start can
// reach, so it is refused rather than reused.
func ValidateCanonicalServerModule(data []byte) error {
	marker := []byte("const manifest = {\n")
	start := bytes.Index(data, marker)
	if start < 0 || bytes.Count(data, marker) != 1 {
		return errors.New("unsupported assistant build manifest")
	}
	start += len("const manifest = ")
	end := bytes.Index(data[start:], []byte("\n};"))
	if end < 0 {
		return errors.New("unterminated assistant build manifest")
	}
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(data[start:start+end+2], &manifest); err != nil {
		return fmt.Errorf("assistant build manifest: %w", err)
	}
	for key, want := range map[string]string{"appRoot": CanonicalRoot, "agentRoot": CanonicalRoot + "/agent"} {
		var value string
		if json.Unmarshal(manifest[key], &value) != nil || value != want {
			return fmt.Errorf("assistant build manifest %s is not canonical", key)
		}
	}
	var static []map[string]json.RawMessage
	if raw, ok := manifest["connections"]; ok {
		if err := json.Unmarshal(raw, &static); err != nil {
			return fmt.Errorf("assistant build manifest connections: %w", err)
		}
	}
	for _, connection := range static {
		var name string
		if json.Unmarshal(connection["connectionName"], &name) == nil && name == "scenery" {
			return errors.New("assistant build manifest Scenery connection is static")
		}
	}
	var dynamic []map[string]json.RawMessage
	if err := json.Unmarshal(manifest["dynamicConnections"], &dynamic); err != nil {
		return fmt.Errorf("assistant build manifest dynamic connections: %w", err)
	}
	count := 0
	for _, connection := range dynamic {
		var slug string
		if json.Unmarshal(connection["slug"], &slug) == nil && slug == "scenery" {
			count++
		}
	}
	if count != 1 {
		return errors.New("assistant build manifest Scenery connection missing or duplicated")
	}
	return nil
}
