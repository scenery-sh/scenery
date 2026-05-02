package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
)

var (
	onlavaVersion = "dev"
	onlavaCommit  = ""
	onlavaBuiltAt = ""
)

type versionResponse struct {
	SchemaVersion string `json:"schema_version"`
	Version       string `json:"version"`
	Commit        string `json:"commit,omitempty"`
	BuiltAt       string `json:"built_at,omitempty"`
	GoVersion     string `json:"go_version"`
	ModuleVersion string `json:"module_version,omitempty"`
}

func versionCommand(args []string) error {
	jsonOutput := false
	for _, arg := range args {
		switch arg {
		case "--json", "-json":
			jsonOutput = true
		default:
			return fmt.Errorf("unknown flag %q", arg)
		}
	}
	resp := buildVersionResponse()
	if jsonOutput {
		return writeVersionJSON(os.Stdout, resp)
	}
	if resp.Commit != "" {
		_, err := fmt.Fprintf(os.Stdout, "onlava %s (%s)\n", resp.Version, resp.Commit)
		return err
	}
	_, err := fmt.Fprintf(os.Stdout, "onlava %s\n", resp.Version)
	return err
}

func buildVersionResponse() versionResponse {
	resp := versionResponse{
		SchemaVersion: "onlava.version.v1",
		Version:       onlavaVersion,
		Commit:        onlavaCommit,
		BuiltAt:       onlavaBuiltAt,
		GoVersion:     runtime.Version(),
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
	return resp
}

func writeVersionJSON(w io.Writer, resp versionResponse) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(resp)
}
