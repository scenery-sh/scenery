package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/toolchain"
)

var (
	sceneryVersion = "dev"
	sceneryCommit  = ""
	sceneryBuiltAt = ""
)

type versionResponse struct {
	cliPayloadIdentity
	Version       string                    `json:"version"`
	Commit        string                    `json:"commit,omitempty"`
	BuiltAt       string                    `json:"built_at,omitempty"`
	GoVersion     string                    `json:"go_version"`
	ModuleVersion string                    `json:"module_version,omitempty"`
	Toolchain     *toolchainManifestVersion `json:"toolchain_manifest,omitempty"`
}

type toolchainManifestVersion struct {
	Kind            string `json:"kind"`
	SchemaRevision  string `json:"schema_revision"`
	SHA256          string `json:"sha256"`
	ArtifactCount   int    `json:"artifact_count"`
	SourceLockCount int    `json:"source_lock_count"`
}

func versionCommand(args []string) error {
	jsonOutput := false
	flags := newCLIFlagSet("version")
	registerJSONOutput(flags, &jsonOutput)
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return err
	}
	resp := buildVersionResponse()
	if jsonOutput {
		return writeVersionJSON(os.Stdout, resp)
	}
	if resp.Commit != "" {
		_, err := fmt.Fprintf(os.Stdout, "scenery %s (%s)\n", resp.Version, resp.Commit)
		return err
	}
	_, err = fmt.Fprintf(os.Stdout, "scenery %s\n", resp.Version)
	return err
}

func buildVersionResponse() versionResponse {
	resp := versionResponse{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.version"),
		Version:            sceneryVersion,
		Commit:             sceneryCommit,
		BuiltAt:            sceneryBuiltAt,
		GoVersion:          runtime.Version(),
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		resp.ModuleVersion = info.Main.Version
		if resp.Version == "" || resp.Version == "dev" {
			if info.Main.Version != "" && info.Main.Version != "(devel)" {
				resp.Version = info.Main.Version
			} else {
				resp.Version = "dev"
			}
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if resp.Commit == "" {
					resp.Commit = setting.Value
				}
			case "vcs.time":
				if resp.BuiltAt == "" {
					resp.BuiltAt = setting.Value
				}
			}
		}
	}
	if resp.Version == "" {
		resp.Version = "dev"
	}
	if manifest, err := toolchain.LoadBundledManifest(); err == nil {
		resp.Toolchain = &toolchainManifestVersion{
			Kind:            manifest.Kind,
			SchemaRevision:  manifest.SchemaRevision,
			SHA256:          toolchain.BundledManifestSHA256(),
			ArtifactCount:   len(manifest.Artifacts),
			SourceLockCount: len(manifest.SourceLocks),
		}
	}
	return resp
}

func cliBuildIdentity() localagent.Identity {
	resp := buildVersionResponse()
	return localagent.Identity{
		Version: resp.Version,
		Commit:  resp.Commit,
		BuiltAt: resp.BuiltAt,
	}
}

func cliProducer() machine.Producer {
	resp := buildVersionResponse()
	producer := machine.Producer{
		Version: resp.Version,
		Commit:  resp.Commit,
		BuiltAt: resp.BuiltAt,
		Toolchain: machine.Toolchain{
			GoVersion: resp.GoVersion,
		},
	}
	if resp.Toolchain != nil && resp.Toolchain.SHA256 != "" {
		producer.Toolchain.ManifestRevision = "sha256:" + resp.Toolchain.SHA256
	}
	return producer
}

func writeVersionJSON(w io.Writer, resp versionResponse) error {
	return writeCLIJSON(w, resp)
}
