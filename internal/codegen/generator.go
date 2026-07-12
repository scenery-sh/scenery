package codegen

import (
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/model"
)

type Output struct {
	Generated map[string][]byte
}

func Generate(appModel *model.App, cfg appcfg.Config, compositionImport string) (*Output, error) {
	out := &Output{Generated: map[string][]byte{}}
	mainFile, err := generateMain(appModel, cfg, compositionImport)
	if err != nil {
		return nil, err
	}
	out.Generated["scenery_internal_main/main.go"] = mainFile
	return out, nil
}
