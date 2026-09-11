package codegen

import (
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
)

type Output struct {
	Generated map[string][]byte
}

func Generate(appName string, cfg appcfg.Config, compositionImport string, sql compiler.SQLRequirements) (*Output, error) {
	out := &Output{Generated: map[string][]byte{}}
	mainFile, err := generateMain(appName, cfg, compositionImport, sql)
	if err != nil {
		return nil, err
	}
	out.Generated["scenery_internal_main/main.go"] = mainFile
	return out, nil
}
