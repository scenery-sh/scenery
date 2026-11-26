package main

import (
	"os"
	"path/filepath"
	"scenery.sh/internal/uigen"
)

func main() {
	assistants := []uigen.Resource{{Address: "app/assistant/support", Module: "app", Name: "support", Kind: "scenery.assistant", Spec: map[string]any{"surface": map[string]any{"path": "/assistants/support"}}}}
	files, _, err := uigen.Render(&uigen.Input{Manifest: &uigen.Manifest{}}, uigen.Resource{Spec: map[string]any{"react": map[string]any{}}}, "internal/uigen/testdata/assistant", nil, assistants, nil)
	if err != nil {
		panic(err)
	}
	for _, f := range files {
		if filepath.Base(f.Path) == "assistants.generated.tsx" {
			if err := os.WriteFile(f.Path, f.Bytes, 0644); err != nil {
				panic(err)
			}
		}
	}
}
