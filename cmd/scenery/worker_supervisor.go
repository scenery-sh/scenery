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

func projectWorkerLaunch(program workerProgram) (workerLaunchProgram, error) {
	environment := envWithoutKeys(minimalAppProcessEnv(envpolicy.Environ()), "SCENERY_LISTEN_NETWORK", "SCENERY_LISTEN_ADDR",
		"SCENERY_PROCESS_LINK", workerruntime.ConfigSnapshotFDEnv, storageconfig.RuntimeConfigEnv)
	launch := workerLaunchProgram{
		Service: program.Service, Process: program.Process, Binary: program.Binary,
		Digest: program.Digest, Runtime: program.Runtime, Identity: program.Identity,
		Environment: environment,
	}
	if program.Runtime != "go" {
		launch.Environment = envWithoutKeys(environment, appDatabaseURLEnv, postgresdb.RegistryEnv)
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
