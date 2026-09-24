package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"scenery.sh/internal/appconfig"
)

// deploymentActiveRecord is ~/.scenery/deployments/<app>/<env>/active.json:
// the successfully activated release and the exact configuration revision it
// runs. Boot resume and restarts use it, never the desired configuration.
type deploymentActiveRecord struct {
	Kind         string `json:"kind"`
	AppID        string `json:"app_id"`
	Environment  string `json:"environment"`
	DeploymentID string `json:"deployment_id"`
	// State is activating until readiness and publication confirm the
	// release, then active. Both states name the installed pair.
	State           string `json:"state"`
	ConfigRevision  string `json:"config_revision"`
	CatalogRevision string `json:"catalog_revision"`
	SourceRoot      string `json:"source_root"`
	ActivatedAt     string `json:"activated_at"`
	Previous        string `json:"previous_deployment_id,omitempty"`
}

const deploymentActiveKind = "scenery.deployment.active"

func deploymentStateDir(home, appID, environment string) string {
	return filepath.Join(home, "deployments", appID, environment)
}

// readDeploymentActive returns the active record, or nil when the environment
// was never activated on this host.
func readDeploymentActive(home, appID, environment string) (*deploymentActiveRecord, error) {
	if !appconfig.ValidIdentifier(appID) || !appconfig.ValidIdentifier(environment) {
		return nil, fmt.Errorf("deployment identity %q/%q is not path-safe", appID, environment)
	}
	data, err := os.ReadFile(filepath.Join(deploymentStateDir(home, appID, environment), "active.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record deploymentActiveRecord
	if err := json.Unmarshal(data, &record); err != nil || record.Kind != deploymentActiveKind || record.AppID != appID || record.Environment != environment {
		return nil, fmt.Errorf("active deployment record of %s/%s is malformed", appID, environment)
	}
	return &record, nil
}
