package codegen

import (
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/model"
)

type Output struct {
	Rewritten map[string][]byte
	Generated map[string][]byte
}

type Options struct {
	CompositionImport string
}

func GenerateWithOptions(appModel *model.App, cfg appcfg.Config, options Options) (*Output, error) {
	out := &Output{Rewritten: map[string][]byte{}, Generated: map[string][]byte{}}
	mainFile, err := generateMain(appModel, cfg, options)
	if err != nil {
		return nil, err
	}
	out.Generated["scenery_internal_main/main.go"] = mainFile
	return out, nil
}
