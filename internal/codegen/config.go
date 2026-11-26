package codegen

import (
	"fmt"
	"go/format"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	generateapi "scenery.sh/internal/generate/api"
)

// generateWorkerMain renders the entrypoint of one service's Worker ABI v1
// process: its SQL bindings, its adapter's contract registry, and the worker
// listener. The host owns authentication; with standard authentication the
// worker reads its records for CurrentUser and memberships.
func generateWorkerMain(appName string, cfg appcfg.Config, service generateapi.ServiceProcessPlan, sql compiler.SQLRequirements) ([]byte, error) {
	var buf strings.Builder
	buf.WriteString("package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n")
	if cfg.Auth.Enabled {
		buf.WriteString("\tsceneryauth \"scenery.sh/auth\"\n")
	}
	buf.WriteString("\tsceneryruntime \"scenery.sh/runtime\"\n")
	fmt.Fprintf(&buf, "\tscenerycomposition %q\n)\n\n", service.AdapterImport)
	buf.WriteString("func main() {\n")
	renderSQLBindings(&buf, sql)
	if cfg.Auth.Enabled {
		buf.WriteString("\tsceneryauth.UseStandard()\n")
	}
	buf.WriteString("\tif err := sceneryruntime.VerifyLinkedContractBundle(scenerycomposition.ContractRevision); err != nil {\n\t\t_, _ = fmt.Fprintf(os.Stderr, \"scenery: %v\\n\", err)\n\t\tos.Exit(1)\n\t}\n")
	fmt.Fprintf(&buf, "\tcontractRegistry, err := sceneryruntime.NewContractRegistry(sceneryruntime.ContractRegistryOptions{ContractRevisions: scenerycomposition.ContractRevisions, RequiredAddresses: %#v, ProviderABIs: sceneryruntime.ContractProviderABIs()})\n", service.RequiredAddresses)
	buf.WriteString("\tif err == nil { err = scenerycomposition.Register(contractRegistry) }\n")
	buf.WriteString("\tif err == nil { err = contractRegistry.Seal() }\n")
	buf.WriteString("\tif err != nil {\n\t\t_, _ = fmt.Fprintf(os.Stderr, \"scenery: %v\\n\", err)\n\t\tos.Exit(1)\n\t}\n")
	fmt.Fprintf(&buf, "\tif err := sceneryruntime.MainGoWorker(%s, %q); err != nil {\n", appConfigLiteral(appName, cfg), service.Address)
	buf.WriteString("\t\t_, _ = fmt.Fprintf(os.Stderr, \"scenery: %v\\n\", err)\n\t\tos.Exit(1)\n\t}\n}\n")
	return format.Source([]byte(buf.String()))
}

func renderSQLBindings(buf *strings.Builder, sql compiler.SQLRequirements) {
	if len(sql) == 0 {
		return
	}
	buf.WriteString("\tif err := sceneryruntime.ConfigureSQLBindings([]sceneryruntime.SQLBinding{\n")
	for _, binding := range sql.Bindings() {
		fmt.Fprintf(buf, "\t\t{Name: %q, Schema: %q},\n", binding.Name, binding.Schema)
	}
	buf.WriteString("\t}); err != nil {\n\t\t_, _ = fmt.Fprintf(os.Stderr, \"scenery: %v\\n\", err)\n\t\tos.Exit(1)\n\t}\n")
}

func appConfigLiteral(appName string, _ appcfg.Config) string {
	return fmt.Sprintf("sceneryruntime.AppConfig{Name: %q, ListenAddr: sceneryruntime.ListenAddrFromEnv()}", appName)
}
