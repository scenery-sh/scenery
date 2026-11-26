package main

import (
	"encoding/json"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/storageconfig"
	workerruntime "scenery.sh/runtime"
)

type workerLaunchProgram struct {
	Service     string                `json:"service"`
	Process     string                `json:"process"`
	Binary      string                `json:"binary"`
	Digest      string                `json:"digest"`
	Runtime     string                `json:"runtime"`
	Identity    workerProcessIdentity `json:"identity"`
	Environment []string              `json:"environment"`
	Startup     json.RawMessage       `json:"startup,omitempty"`
}

func workerProcessEnv(inherited []string, runtime string) []string {
	env := envWithoutKeys(
		minimalAppProcessEnv(inherited),
		"SCENERY_ROLE",
		"SCENERY_LISTEN_NETWORK",
		"SCENERY_LISTEN_ADDR",
		"SCENERY_PROCESS_LINK",
		workerruntime.ConfigSnapshotFDEnv,
		"SCENERY_ASSISTANT_RUNTIME_CONFIG",
		assistantTokenKeyEnv,
		assistantTokenKeyFileEnv,
		"SCENERY_ASSISTANT_ID",
		"SCENERY_ASSISTANT_CONTROL_ADDR",
		"SCENERY_ASSISTANT_CONTROL_TOKEN",
		"SCENERY_MCP_BRIDGE_SECRET",
		"SCENERY_MCP_URL",
	)
	if runtime != "go" {
		// Go services keep ambient scenery.sh/storage access to the app's
		// configured stores; other workers use declared Host capabilities.
		env = envWithoutKeys(env, storageconfig.RuntimeConfigEnv, appDatabaseURLEnv, postgresdb.RegistryEnv, "SCENERY_DEV_REPORT_URL", "SCENERY_DEV_REPORT_TOKEN")
	}
	return env
}

func projectWorkerLaunch(program workerProgram) (workerLaunchProgram, error) {
	environment := workerProcessEnv(envpolicy.Environ(), program.Runtime)
	launch := workerLaunchProgram{
		Service: program.Service, Process: program.Process, Binary: program.Binary,
		Digest: program.Digest, Runtime: program.Runtime, Identity: program.Identity,
		Environment: environment,
	}
	if compiler.IsJSRuntime(program.Runtime) {
		data, err := jsWorkerStartup(program)
		if err != nil {
			return workerLaunchProgram{}, err
		}
		launch.Startup = data
	} else if program.Runtime == "go" && program.config != nil {
		data, err := json.Marshal(program.config)
		if err != nil {
			return workerLaunchProgram{}, err
		}
		launch.Startup = data
	}
	return launch, nil
}
