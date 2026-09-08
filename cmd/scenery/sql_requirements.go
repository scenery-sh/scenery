package main

import (
	"fmt"

	"scenery.sh/internal/compiler"
)

// A command with no existing build result compiles once. Build/runtime callers
// pass Result.SQLRequirements directly instead of rediscovering app facts.
func compileSQLRequirements(root string) (compiler.SQLRequirements, error) {
	result, err := compileSQLContract(root)
	if err != nil {
		return nil, err
	}
	return result.SQLRequirements, nil
}

func compileSQLContract(root string) (*compiler.Result, error) {
	result, err := compiler.Compile(root)
	if err != nil {
		return nil, err
	}
	if !result.Valid() {
		for _, diagnostic := range result.Diagnostics {
			if diagnostic.Severity == "error" {
				return nil, &codedCLIError{code: 3, err: fmt.Errorf("%s: %s; fix the current application source before resolving SQL requirements", diagnostic.Code, diagnostic.Message)}
			}
		}
		return nil, &codedCLIError{code: 3, err: fmt.Errorf("current application source is invalid; run scenery check before resolving SQL requirements")}
	}
	return result, nil
}

// SQL supply validation is pure and always precedes the allocation owner. An
// external or attached declaration never grants implicit provisioning authority.
func resolveSQLSupply(requirements compiler.SQLRequirements, env []string, allowManaged bool) ([]compiler.SQLBinding, error) {
	remoteDurable := lookupEnvValue(env, "SCENERY_DURABLE_ENDPOINT") != ""
	bindings := requirements.Bindings(remoteDurable)
	if len(bindings) == 0 {
		return bindings, nil
	}
	if value := lookupEnvValue(env, appDatabaseURLEnv); value != "" {
		if err := validateAppPostgresURL(value); err != nil {
			return nil, err
		}
		return bindings, nil
	}
	if !allowManaged {
		return nil, &codedCLIError{code: 3, err: fmt.Errorf("app SQL requirements need DATABASE_URL for scenery worker; supply it through the selected environment; managed PostgreSQL is a scenery up development capability")}
	}
	for _, requirement := range requirements {
		if remoteDurable && requirement.Kind == compiler.SQLDurable {
			continue
		}
		if requirement.Lifecycle != "managed" {
			return nil, &codedCLIError{code: 3, err: fmt.Errorf("SQL requirement %s has lifecycle %q, which does not authorize managed provisioning; supply an explicit DATABASE_URL or intentionally declare managed lifecycle in source", requirement.Address, requirement.Lifecycle)}
		}
	}
	return bindings, nil
}
