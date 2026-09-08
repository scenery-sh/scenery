package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/spec"
)

// Existing product launch marker, removed from fixture parent environments.
const detachedDevChildEnv = "SCENERY_DEV_DETACHED_CHILD"

type productCommandRunner func(context.Context, string, io.Writer, ...string) error

// runProduct invokes only the prepared binary belonging to this repository.
// The caller owns fixture environment and lifecycle; there is no PATH fallback.
func runProduct(ctx context.Context, repoRoot string, stdout io.Writer, args ...string) error {
	binary := harnessLocalSceneryBinaryPath(repoRoot)
	if !filepath.IsAbs(binary) {
		return fmt.Errorf("prepared product binary must be absolute: %s", binary)
	}
	cmd := commandTreeContext(ctx, binary, args...)
	cmd.Dir = repoRoot
	cmd.Stdout = stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("product %v: %w: %s", args, err, tailString(stderr.String(), 8192))
	}
	return nil
}

func readProductJSON(repoRoot string, target any, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var output bytes.Buffer
	if err := runProduct(ctx, repoRoot, &output, args...); err != nil {
		return productFailure(err, output.Bytes())
	}
	return decodeCLIJSON(output.Bytes(), target)
}

func productFailure(err error, output []byte) error {
	if envelope, decodeErr := machine.Decode[graph.Diagnostic](output, currentMachineSpecRevision()); decodeErr == nil && len(envelope.Diagnostics) > 0 {
		var messages []string
		for _, diagnostic := range envelope.Diagnostics {
			messages = append(messages, diagnostic.Code+": "+diagnostic.Message)
		}
		return fmt.Errorf("%w: %s", err, strings.Join(messages, "; "))
	}
	return err
}

func runSceneryInspect(args []string, stdout io.Writer) error {
	root := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--repo-root" {
			root = args[i+1]
		}
	}
	root, err := discoverSceneryRepoRoot(root)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return runProduct(ctx, root, stdout, append([]string{"inspect"}, args...)...)
}

func buildInspectDocsResponse(repoRoot string) (inspectDocsResponse, error) {
	var result inspectDocsResponse
	err := readProductJSON(repoRoot, &result, "inspect", "docs", "--repo-root", repoRoot, "--all", "-o", "json")
	return result, err
}

func decodeCLIJSON(encoded []byte, target any) error {
	return machine.DecodeData[graph.Diagnostic](encoded, currentMachineSpecRevision(), target)
}

func currentMachineSpecRevision() string { return string(spec.CurrentRevision()) }

// Verifier reports identify their writer, not the separately probed product.
func cliProducer() machine.Producer {
	p := machine.Producer{Version: "dev", Toolchain: machine.Toolchain{GoVersion: runtime.Version()}}
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			p.Version = info.Main.Version
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				p.Commit = setting.Value
			case "vcs.time":
				p.BuiltAt = setting.Value
			}
		}
	}
	return p
}

func newCLIEnvelope(ok bool, data any, diagnostics []graph.Diagnostic) machine.Envelope[graph.Diagnostic] {
	return machine.NewEnvelope(currentMachineSpecRevision(), cliProducer(), ok, data, diagnostics)
}

func writeCLIJSON(w io.Writer, data any) error {
	ok := true
	switch value := data.(type) {
	case harnessSelfResponse:
		ok = value.OK
	case harnessSelfSummaryResponse:
		ok = value.OK
	}
	return json.NewEncoder(w).Encode(newCLIEnvelope(ok, data, nil))
}

// These views consume current product output; the schemas remain authoritative.
type inspectHarnessResponse struct {
	Evidence []harnessEvidence `json:"evidence"`
}

func readProductHarness(repoRoot string) (inspectHarnessResponse, error) {
	var result inspectHarnessResponse
	err := readProductJSON(repoRoot, &result, "inspect", "harness", "--repo-root", repoRoot, "-o", "json")
	return result, err
}
