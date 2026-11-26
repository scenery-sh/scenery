package codegen

import (
	"fmt"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	generateapi "scenery.sh/internal/generate/api"
)

// ProcessMainRoot holds one Worker ABI v1 entrypoint per native Go service
// at services/<name>/main.go.
const ProcessMainRoot = "scenery_internal_processes"

type Output struct {
	Generated map[string][]byte
}

func Generate(appName string, cfg appcfg.Config, plan generateapi.RuntimeIntegrationPlan, sql compiler.SQLRequirements) (*Output, error) {
	out := &Output{Generated: map[string][]byte{}}
	for _, service := range plan.Services {
		path := ProcessMainRoot + "/services/" + service.Name + "/main.go"
		if _, exists := out.Generated[path]; exists || service.Name == "" || service.AdapterImport == "" || len(service.RequiredAddresses) == 0 {
			return nil, fmt.Errorf("invalid service process plan for %q", service.Address)
		}
		serviceMain, err := generateWorkerMain(appName, cfg, service, sql)
		if err != nil {
			return nil, fmt.Errorf("render service process entrypoint %s: %w", service.Address, err)
		}
		out.Generated[path] = serviceMain
	}
	return out, nil
}
