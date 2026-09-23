package generate

import (
	_ "embed"
	"path/filepath"
	"strings"

	"scenery.sh/internal/machine"
)

// devRuntimeClientSource is the fixed development runtime RPC client. It does
// not depend on the application graph; only the status schema revision it
// verifies is substituted at generation time.
//
//go:embed dev_runtime_client.ts
var devRuntimeClientSource string

const devRuntimeStatusRevisionPlaceholder = "{{dev_runtime_status_revision}}"

func typeScriptDevRuntimeEnabled(target Resource) bool {
	enabled, _ := target.Spec["dev_runtime"].(bool)
	return enabled
}

func renderTypeScriptDevRuntimeFile(root string) generatedFile {
	revision, ok := machine.PayloadSchemaRevision("scenery.dev-runtime.status")
	if !ok {
		panic("missing scenery.dev-runtime.status schema revision")
	}
	source := strings.Replace(devRuntimeClientSource, devRuntimeStatusRevisionPlaceholder, revision, 1)
	return generatedFile{Path: filepath.Join(root, "dev-runtime.ts"), Bytes: []byte(source)}
}
