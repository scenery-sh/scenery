package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
)

// This file is also copied into the disposable ONLV artifact. It is an
// experiment protocol, not a supported application execution ABI.
const nativeReloadABI = "scenery.native-reload-experiment"
const nativeReloadOperation = "ahjs/operation/list_ahjs"
const nativeReloadBinding = "ahjs/binding/list_ahjs_http"
const nativeReloadFrameLimit = 64 << 10

type nativeReloadIdentity struct {
	ABI                 string `json:"abi"`
	ProtocolRevision    string `json:"protocol_revision"`
	FrameworkSource     string `json:"framework_source"`
	FrameworkExecutable string `json:"framework_executable"`
	ContractABI         string `json:"contract_abi"`
	BuildInputs         string `json:"build_inputs"`
	Implementation      string `json:"implementation"`
	ExecutionGeneration string `json:"execution_generation"`
	ExecutableDigest    string `json:"executable_digest"`
	Worktree            string `json:"worktree"`
	Session             string `json:"session"`
	Toolchain           string `json:"toolchain"`
	Target              string `json:"target"`
}

type nativeReloadFrame struct {
	Kind            string               `json:"kind"`
	Identity        nativeReloadIdentity `json:"identity"`
	PID             int                  `json:"pid"`
	Nonce           string               `json:"nonce,omitempty"`
	Operation       string               `json:"operation,omitempty"`
	Binding         string               `json:"binding,omitempty"`
	Input           json.RawMessage      `json:"input,omitempty"`
	Outcome         json.RawMessage      `json:"outcome,omitempty"`
	Failure         string               `json:"failure,omitempty"`
	Constructed     bool                 `json:"constructed"`
	FailConstructor bool                 `json:"fail_constructor,omitempty"`
	ConstructorMS   float64              `json:"constructor_ms,omitempty"`
	SelfHashMS      float64              `json:"self_hash_ms,omitempty"`
	MainToAttestMS  float64              `json:"main_to_attest_ms,omitempty"`
}

func nativeReloadCheckIdentity(want, got nativeReloadIdentity) error {
	if want.ABI != nativeReloadABI || want.Worktree == "" || want.Session == "" || want.Toolchain == "" || want.Target == "" {
		return fmt.Errorf("incomplete expected experimental identity")
	}
	for _, digest := range []string{want.ProtocolRevision, want.FrameworkSource, want.FrameworkExecutable, want.ContractABI, want.BuildInputs, want.Implementation, want.ExecutionGeneration, want.ExecutableDigest} {
		if len(digest) != 71 || digest[:7] != "sha256:" {
			return fmt.Errorf("invalid expected identity digest")
		}
		if _, err := hex.DecodeString(digest[7:]); err != nil {
			return fmt.Errorf("invalid expected identity digest: %w", err)
		}
	}
	if want != got {
		return fmt.Errorf("artifact, generation, contract, producer or owner mismatch")
	}
	return nil
}

func nativeReloadDecodeFrame(data []byte) (nativeReloadFrame, error) {
	var frame nativeReloadFrame
	if len(data) > nativeReloadFrameLimit {
		return frame, fmt.Errorf("experimental frame exceeds limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&frame); err != nil {
		return frame, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return frame, fmt.Errorf("experimental frame has trailing data")
	}
	return frame, nil
}

func nativeReloadDigest(data []byte) string {
	value := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(value[:])
}

func nativeReloadFileDigest(path string) (string, error) {
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

// Nearest-rank quantiles match the existing edit benchmark. Small series are
// labelled as early evidence, never as a 30-edit acceptance distribution.
func nativeReloadStats(values []float64) map[string]any {
	if len(values) == 0 {
		return map[string]any{"count": 0}
	}
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	var sum float64
	for _, value := range ordered {
		sum += value
	}
	return map[string]any{"count": len(ordered), "p50_ms": ordered[(len(ordered)-1)/2],
		"p95_ms": ordered[(95*len(ordered)+99)/100-1], "worst_ms": ordered[len(ordered)-1], "mean_ms": sum / float64(len(ordered))}
}
