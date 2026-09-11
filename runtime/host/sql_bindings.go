package host

import (
	"scenery.sh/internal/nativesql"
	"scenery.sh/internal/postgresdb"
)

type SQLBinding = nativesql.Binding

func ConfigureSQLBindings(bindings []SQLBinding) error { return nativesql.Configure(bindings) }
func resolveRuntimeSQLBindings(bindings []SQLBinding, lookup func(string) string) (postgresdb.Database, error) {
	return nativesql.Resolve(bindings, lookup)
}
