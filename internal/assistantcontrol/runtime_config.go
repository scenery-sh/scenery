package assistantcontrol

// The private assistant runtime descriptor is the handoff from the CLI's
// assistant supervisor to the host: one file names each assistant's helper
// control endpoint, token and MCP bridge.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"scenery.sh/internal/contract"
)

const (
	runtimeConfigLimit    = int64(1 << 20)
	maxRuntimeDescriptors = 256
	mcpBridgeSecretMin    = 32
)

var errRuntimeConfig = errors.New("assistant runtime configuration is invalid")

// HelperDescriptor is one private helper endpoint descriptor.
// ControlAddress and ControlToken are server-only values.  MarshalJSON omits
// both so status, diagnostics, and accidental config serialization cannot
// disclose a private address or credential.
type HelperDescriptor struct {
	AssistantAddress   string `json:"assistant_address"`
	ControlAddress     string `json:"-"`
	ControlToken       string `json:"-"`
	MCPListenAddress   string `json:"-"`
	MCPBridgeSecret    string `json:"-"`
	RuntimeRevision    string `json:"runtime_revision"`
	CapabilityRevision string `json:"capability_revision"`
	Required           bool   `json:"required"`
}

// RuntimeConfig is the strict provider-neutral descriptor map
// supplied by the supervisor/production launcher.  A missing file means all
// declared assistants remain registered but unavailable.
type RuntimeConfig struct {
	Assistants []HelperDescriptor `json:"assistants"`
}

type helperDescriptorWire struct {
	AssistantAddress   string `json:"assistant_address"`
	ControlAddress     string `json:"control_address"`
	ControlToken       string `json:"control_token"`
	MCPListenAddress   string `json:"mcp_listen_address"`
	MCPBridgeSecret    string `json:"mcp_bridge_secret"`
	RuntimeRevision    string `json:"runtime_revision"`
	CapabilityRevision string `json:"capability_revision"`
	Required           bool   `json:"required"`
}

type runtimeConfigWire struct {
	Assistants []helperDescriptorWire `json:"assistants"`
}

// UnmarshalJSON accepts private descriptor fields only at the server-side
// config boundary.  The custom method is deliberately paired with
// MarshalJSON below so credentials and loopback addresses never serialize.
func (descriptor *HelperDescriptor) UnmarshalJSON(data []byte) error {
	type wire struct {
		AssistantAddress   string `json:"assistant_address"`
		ControlAddress     string `json:"control_address"`
		ControlToken       string `json:"control_token"`
		MCPListenAddress   string `json:"mcp_listen_address"`
		MCPBridgeSecret    string `json:"mcp_bridge_secret"`
		RuntimeRevision    string `json:"runtime_revision"`
		CapabilityRevision string `json:"capability_revision"`
		Required           *bool  `json:"required"`
	}
	var value wire
	if err := decodeStrictJSON(data, &value); err != nil {
		return err
	}
	required := true
	if value.Required != nil {
		required = *value.Required
	}
	*descriptor = HelperDescriptor{
		AssistantAddress:   strings.TrimSpace(value.AssistantAddress),
		ControlAddress:     strings.TrimSpace(value.ControlAddress),
		ControlToken:       value.ControlToken,
		MCPListenAddress:   strings.TrimSpace(value.MCPListenAddress),
		MCPBridgeSecret:    value.MCPBridgeSecret,
		RuntimeRevision:    strings.TrimSpace(value.RuntimeRevision),
		CapabilityRevision: strings.TrimSpace(value.CapabilityRevision),
		Required:           required,
	}
	return nil
}

// MarshalJSON never includes the private helper address or control token.
func (descriptor HelperDescriptor) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		AssistantAddress   string `json:"assistant_address"`
		RuntimeRevision    string `json:"runtime_revision"`
		CapabilityRevision string `json:"capability_revision"`
		Required           bool   `json:"required"`
	}{
		AssistantAddress:   descriptor.AssistantAddress,
		RuntimeRevision:    descriptor.RuntimeRevision,
		CapabilityRevision: descriptor.CapabilityRevision,
		Required:           descriptor.Required,
	})
}

// Validate checks the complete descriptor set before any client is created.
// It does not probe the helper; an unavailable or revision-mismatched helper
// is represented as an unavailable assistant while the Go app remains alive.
func (config RuntimeConfig) Validate() error {
	if len(config.Assistants) > maxRuntimeDescriptors {
		return fmt.Errorf("%w: too many assistants", errRuntimeConfig)
	}
	seen := make(map[string]struct{}, len(config.Assistants))
	for index, descriptor := range config.Assistants {
		address := strings.TrimSpace(descriptor.AssistantAddress)
		if address == "" {
			return fmt.Errorf("%w: assistant %d has no assistant_address", errRuntimeConfig, index)
		}
		if len(address) > 256 || strings.IndexFunc(address, func(r rune) bool { return r <= 0x20 || r == '\\' || r == '?' || r == '#' }) >= 0 {
			return fmt.Errorf("%w: assistant %d has an invalid assistant_address", errRuntimeConfig, index)
		}
		if _, exists := seen[address]; exists {
			return fmt.Errorf("%w: duplicate assistant_address", errRuntimeConfig)
		}
		seen[address] = struct{}{}
		if strings.TrimSpace(descriptor.ControlAddress) == "" {
			return fmt.Errorf("%w: assistant %s has no control_address", errRuntimeConfig, address)
		}
		if strings.TrimSpace(descriptor.ControlToken) == "" {
			return fmt.Errorf("%w: assistant %s has no control_token", errRuntimeConfig, address)
		}
		if (strings.TrimSpace(descriptor.MCPListenAddress) == "") != (strings.TrimSpace(descriptor.MCPBridgeSecret) == "") {
			return fmt.Errorf("%w: assistant %s has incomplete MCP bridge descriptor", errRuntimeConfig, address)
		}
		if descriptor.MCPListenAddress != "" {
			if err := validateMCPListenAddress(descriptor.MCPListenAddress); err != nil {
				return fmt.Errorf("%w: assistant %s MCP listen address: %v", errRuntimeConfig, address, err)
			}
			if len(descriptor.MCPBridgeSecret) < mcpBridgeSecretMin || len(descriptor.MCPBridgeSecret) > 4096 || strings.TrimSpace(descriptor.MCPBridgeSecret) == "" || strings.IndexFunc(descriptor.MCPBridgeSecret, func(r rune) bool { return r <= 0x20 }) >= 0 {
				return fmt.Errorf("%w: assistant %s MCP bridge secret is invalid", errRuntimeConfig, address)
			}
		}
		if strings.TrimSpace(descriptor.RuntimeRevision) == "" || strings.TrimSpace(descriptor.CapabilityRevision) == "" {
			return fmt.Errorf("%w: assistant %s has incomplete revisions", errRuntimeConfig, address)
		}
		if err := validateDescriptorRevision(descriptor.RuntimeRevision); err != nil {
			return fmt.Errorf("%w: assistant %s runtime revision: %v", errRuntimeConfig, address, err)
		}
		if err := validateDescriptorRevision(descriptor.CapabilityRevision); err != nil {
			return fmt.Errorf("%w: assistant %s capability revision: %v", errRuntimeConfig, address, err)
		}
	}
	return nil
}

func validateDescriptorRevision(value string) error {
	if len(value) > 256 || strings.IndexFunc(strings.TrimSpace(value), func(r rune) bool { return r <= 0x20 || r == '\\' || r == '"' }) >= 0 {
		return errors.New("revision is invalid")
	}
	return nil
}

// LoadRuntimeConfig reads one strict descriptor file.  Unknown
// fields, duplicate JSON members, trailing values, and oversized files fail
// closed before any helper client is touched.
func LoadRuntimeConfig(path string) (RuntimeConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return RuntimeConfig{}, nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return RuntimeConfig{}, fmt.Errorf("%w: unable to read descriptor file", errRuntimeConfig)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return RuntimeConfig{}, fmt.Errorf("%w: descriptor file must be a private regular file", errRuntimeConfig)
	}
	file, err := os.Open(path)
	if err != nil {
		return RuntimeConfig{}, fmt.Errorf("%w: unable to read descriptor file", errRuntimeConfig)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, runtimeConfigLimit+1))
	if err != nil {
		return RuntimeConfig{}, fmt.Errorf("%w: unable to read descriptor file", errRuntimeConfig)
	}
	if int64(len(data)) > runtimeConfigLimit {
		return RuntimeConfig{}, fmt.Errorf("%w: descriptor file is too large", errRuntimeConfig)
	}
	var config RuntimeConfig
	if err := decodeStrictJSON(data, &config); err != nil {
		return RuntimeConfig{}, fmt.Errorf("%w: %v", errRuntimeConfig, err)
	}
	if err := config.Validate(); err != nil {
		return RuntimeConfig{}, err
	}
	return config, nil
}

// WriteRuntimeConfig writes the private supervisor descriptor map
// atomically with mode 0600. Unlike MarshalJSON, this explicit server-only
// writer includes control address/token fields because it is the handoff
// boundary to the generated app child; callers must never expose its bytes in
// public responses or diagnostics.
func WriteRuntimeConfig(path string, config RuntimeConfig) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("%w: descriptor path is required", errRuntimeConfig)
	}
	if err := config.Validate(); err != nil {
		return err
	}
	parent := filepath.Dir(path)
	if parent == "." {
		parent = ""
	}
	if parent != "" {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return fmt.Errorf("%w: unable to create descriptor directory", errRuntimeConfig)
		}
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("%w: descriptor path is not a regular file", errRuntimeConfig)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: unable to inspect descriptor path", errRuntimeConfig)
	}
	wire := runtimeConfigWire{Assistants: make([]helperDescriptorWire, 0, len(config.Assistants))}
	for _, descriptor := range config.Assistants {
		wire.Assistants = append(wire.Assistants, helperDescriptorWire(descriptor))
	}
	data, err := json.Marshal(wire)
	if err != nil {
		return fmt.Errorf("%w: encode descriptor file", errRuntimeConfig)
	}
	temporary, err := os.CreateTemp(parentOrCurrent(parent), ".assistant-runtime-*")
	if err != nil {
		return fmt.Errorf("%w: create descriptor staging file", errRuntimeConfig)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("%w: protect descriptor staging file", errRuntimeConfig)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("%w: write descriptor staging file", errRuntimeConfig)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("%w: sync descriptor staging file", errRuntimeConfig)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("%w: close descriptor staging file", errRuntimeConfig)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("%w: install descriptor file", errRuntimeConfig)
	}
	removeTemporary = false
	if directory, err := os.Open(parentOrCurrent(parent)); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func parentOrCurrent(parent string) string {
	if strings.TrimSpace(parent) == "" {
		return "."
	}
	return parent
}

func decodeStrictJSON(data []byte, target any) error {
	if _, err := contract.DecodeJSONObject(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func validateMCPListenAddress(value string) error {
	value = strings.TrimSpace(value)
	host, port, err := net.SplitHostPort(value)
	if err != nil || host != "127.0.0.1" {
		return errors.New("MCP listen address must be 127.0.0.1:<port>")
	}
	parsed, err := strconv.Atoi(port)
	if err != nil || parsed < 1 || parsed > 65535 {
		return errors.New("MCP listen address port is invalid")
	}
	return nil
}
