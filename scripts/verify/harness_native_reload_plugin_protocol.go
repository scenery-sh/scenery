package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const pluginReloadABI = "scenery.native-reload-plugin-experiment"
const pluginReloadFrameLimit = 64 << 10
const pluginReloadOperation = "ahjs/operation/list_ahjs"
const pluginReloadBinding = "ahjs/binding/list_ahjs_http"

type pluginReloadHostBase struct {
	ABI                 string `json:"abi"`
	ProtocolRevision    string `json:"protocol_revision"`
	FrameworkSource     string `json:"framework_source"`
	FrameworkExecutable string `json:"framework_executable"`
	ContractABI         string `json:"contract_abi"`
	HostBuildInputs     string `json:"host_build_inputs"`
	ArtifactRoot        string `json:"artifact_root"`
	Worktree            string `json:"worktree"`
	Session             string `json:"session"`
	Toolchain           string `json:"toolchain"`
	Target              string `json:"target"`
}

type pluginReloadHostIdentity struct {
	Base             pluginReloadHostBase `json:"base"`
	ExecutableDigest string               `json:"executable_digest"`
}

type pluginReloadLinkedIdentity struct {
	Host                pluginReloadHostIdentity `json:"host"`
	BuildInputs         string                   `json:"build_inputs"`
	Implementation      string                   `json:"implementation"`
	ExecutionGeneration string                   `json:"execution_generation"`
}

type pluginReloadIdentity struct {
	Linked         pluginReloadLinkedIdentity `json:"linked"`
	ArtifactDigest string                     `json:"artifact_digest"`
}

type pluginReloadFrame struct {
	Kind            string                   `json:"kind"`
	Host            pluginReloadHostIdentity `json:"host"`
	Identity        pluginReloadIdentity     `json:"identity"`
	PID             int                      `json:"pid"`
	Nonce           string                   `json:"nonce,omitempty"`
	PluginPath      string                   `json:"plugin_path,omitempty"`
	Operation       string                   `json:"operation,omitempty"`
	Binding         string                   `json:"binding,omitempty"`
	Input           json.RawMessage          `json:"input,omitempty"`
	Outcome         json.RawMessage          `json:"outcome,omitempty"`
	Failure         string                   `json:"failure,omitempty"`
	Detail          string                   `json:"detail,omitempty"`
	Constructed     bool                     `json:"constructed"`
	FailConstructor bool                     `json:"fail_constructor,omitempty"`
	OpenMS          float64                  `json:"open_ms,omitempty"`
	ActivationMS    float64                  `json:"activation_ms,omitempty"`
	InvocationMS    float64                  `json:"invocation_ms,omitempty"`
}

func pluginReloadDecodeFrame(data []byte) (pluginReloadFrame, error) {
	var frame pluginReloadFrame
	if len(data) > pluginReloadFrameLimit {
		return frame, fmt.Errorf("plugin experiment frame exceeds limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&frame); err != nil {
		return frame, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return frame, fmt.Errorf("plugin experiment frame has trailing data")
	}
	return frame, nil
}

func pluginReloadCheckHostIdentity(want, got pluginReloadHostIdentity) error {
	if want.Base.ABI != pluginReloadABI || want.Base.ArtifactRoot == "" || want.Base.Worktree == "" || want.Base.Session == "" || want.Base.Toolchain == "" || want.Base.Target == "" {
		return fmt.Errorf("incomplete expected plugin host identity")
	}
	for _, digest := range []string{want.Base.ProtocolRevision, want.Base.FrameworkSource, want.Base.FrameworkExecutable, want.Base.ContractABI, want.Base.HostBuildInputs, want.ExecutableDigest} {
		if err := pluginReloadCheckDigest(digest); err != nil {
			return err
		}
	}
	if want != got {
		return fmt.Errorf("plugin host producer, source, contract or owner mismatch")
	}
	return nil
}

func pluginReloadCheckIdentity(host pluginReloadHostIdentity, want, got pluginReloadIdentity) error {
	if err := pluginReloadCheckHostIdentity(host, want.Linked.Host); err != nil {
		return err
	}
	for _, digest := range []string{want.Linked.BuildInputs, want.Linked.Implementation, want.Linked.ExecutionGeneration, want.ArtifactDigest} {
		if err := pluginReloadCheckDigest(digest); err != nil {
			return err
		}
	}
	derived := want.Linked
	derived.ExecutionGeneration = ""
	data, err := json.Marshal(derived)
	if err != nil || pluginReloadDigest(data) != want.Linked.ExecutionGeneration {
		return fmt.Errorf("plugin execution generation does not match its inputs")
	}
	if want != got {
		return fmt.Errorf("plugin artifact, generation, inputs or owner mismatch")
	}
	return nil
}

func pluginReloadCheckDigest(value string) error {
	if len(value) != 71 || value[:7] != "sha256:" {
		return fmt.Errorf("invalid plugin experiment digest")
	}
	if _, err := hex.DecodeString(value[7:]); err != nil {
		return fmt.Errorf("invalid plugin experiment digest: %w", err)
	}
	return nil
}

func pluginReloadDigest(data []byte) string {
	value := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(value[:])
}

func pluginReloadFileDigest(path string) (string, error) {
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

func pluginReloadPathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !filepath.IsAbs(rel) && !bytes.HasPrefix([]byte(rel), []byte(".."+string(filepath.Separator)))
}
