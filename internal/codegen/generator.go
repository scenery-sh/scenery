package codegen

import (
	"fmt"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	generateapi "scenery.sh/internal/generate/api"
)

// ProcessMainRoot holds the process-per-service entrypoints: the host at
// host/main.go and one service process at services/<name>/main.go.
const ProcessMainRoot = "scenery_internal_processes"

type Output struct {
	Generated map[string][]byte
}

func Generate(appName string, cfg appcfg.Config, plan generateapi.RuntimeIntegrationPlan, sql compiler.SQLRequirements) (*Output, error) {
	out := &Output{Generated: map[string][]byte{}}
	mainFile, err := generateMain(appName, cfg, plan.CompositionImport, sql)
	if err != nil {
		return nil, err
	}
	out.Generated["scenery_internal_main/main.go"] = mainFile
	if len(plan.Services) == 0 {
		return out, nil
	}
	for _, service := range plan.Services {
		path := ProcessMainRoot + "/services/" + service.Name + "/main.go"
		if _, exists := out.Generated[path]; exists || service.Name == "" || service.AdapterImport == "" || len(service.RequiredAddresses) == 0 {
			return nil, fmt.Errorf("invalid service process plan for %q", service.Address)
		}
		serviceMain, err := generateServiceMain(appName, cfg, service, sql)
		if err != nil {
			return nil, fmt.Errorf("render service process entrypoint %s: %w", service.Address, err)
		}
		out.Generated[path] = serviceMain
	}
	hostMain, err := generateHostMain(appName, plan)
	if err != nil {
		return nil, fmt.Errorf("render process host entrypoint: %w", err)
	}
	out.Generated[ProcessMainRoot+"/host/main.go"] = hostMain
	return out, nil
}
