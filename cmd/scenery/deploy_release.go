package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/appconfig"
	"scenery.sh/internal/atomicfile"
)

// Revision-safe SSH deployment keeps durable state out of every synchronized
// tree. On the target:
//
//	~/.scenery/apps/<app-id>/...                         environment store
//	~/.scenery/deployments/<app-id>/<env>/source/        stable runtime root
//	~/.scenery/deployments/<app-id>/<env>/releases/<id>/source/        staged source
//	~/.scenery/deployments/<app-id>/<env>/releases/<id>/receipt.json   release record
//	~/.scenery/deployments/<app-id>/<env>/active.json    installed release
//
// The stable root keeps the runtime's data ownership across releases; only a
// staged release directory is ever an rsync --delete destination.
const (
	deployRequestKind      = "scenery.deploy.request"
	deployResponseKind     = "scenery.deploy.response"
	deployProtocolVersion  = 1
	deployRemoteCommand    = "scenery deploy receive"
	deployReleaseReceipt   = "scenery.deployment.release"
	retainedDeployReleases = 2
)

type deployRemoteRequest struct {
	Kind         string `json:"kind"`
	Protocol     int    `json:"protocol"`
	AppID        string `json:"app_id"`
	Environment  string `json:"environment"`
	Operation    string `json:"operation"`
	DeploymentID string `json:"deployment_id"`
}

type deployRemoteResponse struct {
	Kind           string   `json:"kind"`
	Protocol       int      `json:"protocol"`
	OK             bool     `json:"ok"`
	Code           string   `json:"code,omitempty"`
	Error          string   `json:"error,omitempty"`
	Problems       []string `json:"problems,omitempty"`
	StagingPath    string   `json:"staging_path,omitempty"`
	SourceRoot     string   `json:"source_root,omitempty"`
	ConfigRevision string   `json:"config_revision,omitempty"`
	Previous       string   `json:"previous_deployment_id,omitempty"`
}

// deployReleaseRecord is releases/<id>/receipt.json.
type deployReleaseRecord struct {
	Kind            string `json:"kind"`
	AppID           string `json:"app_id"`
	Environment     string `json:"environment"`
	DeploymentID    string `json:"deployment_id"`
	State           string `json:"state"`
	ConfigRevision  string `json:"config_revision"`
	CatalogRevision string `json:"catalog_revision,omitempty"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

type deployLayout struct {
	home, appID, environment string
}

func (layout deployLayout) state() string {
	return deploymentStateDir(layout.home, layout.appID, layout.environment)
}
func (layout deployLayout) sourceRoot() string { return filepath.Join(layout.state(), "source") }
func (layout deployLayout) release(id string) string {
	return filepath.Join(layout.state(), "releases", id)
}
func (layout deployLayout) legacyRoot() string {
	return filepath.Join(layout.home, "apps", layout.appID)
}

// legacyDeployRootError reports a target whose previous-generation deploy
// synchronized source into the configuration store directory. Its data is
// owned by that root; moving it needs the explicit migration runbook.
func (layout deployLayout) legacyDeployRootError() error {
	if _, err := os.Stat(filepath.Join(layout.legacyRoot(), app.PrimaryConfigFilename)); err != nil {
		return nil
	}
	return fmt.Errorf("the target still runs a legacy deployment checkout at %s, which owns its data and occupies the configuration store; follow docs/runbooks/deploy-root-migration.md to move it before deploying or configuring this application there", layout.legacyRoot())
}

func (layout deployLayout) readRelease(id string) (*deployReleaseRecord, error) {
	data, err := os.ReadFile(filepath.Join(layout.release(id), "receipt.json"))
	if err != nil {
		return nil, err
	}
	var record deployReleaseRecord
	if err := json.Unmarshal(data, &record); err != nil || record.Kind != deployReleaseReceipt || record.DeploymentID != id {
		return nil, fmt.Errorf("release %s receipt is malformed", id)
	}
	return &record, nil
}

func (layout deployLayout) writeRelease(record deployReleaseRecord) error {
	record.Kind, record.UpdatedAt = deployReleaseReceipt, time.Now().UTC().Format(time.RFC3339Nano)
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return writeDeploymentState(filepath.Join(layout.release(record.DeploymentID), "receipt.json"), append(encoded, '\n'))
}

func (layout deployLayout) writeActive(record deploymentActiveRecord) error {
	record.Kind, record.AppID, record.Environment = deploymentActiveKind, layout.appID, layout.environment
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return writeDeploymentState(filepath.Join(layout.state(), "active.json"), append(encoded, '\n'))
}

// deployStateDurable flushes release records; in-process tests clear it and
// the configuration-deploy probe proves the real flush.
var deployStateDurable = true

func writeDeploymentState(path string, data []byte) error {
	return atomicfile.Write(path, data, 0o600, atomicfile.Options{SyncFile: deployStateDurable, SyncDir: deployStateDurable})
}

// deployReceiveCommand serves one deployment request on the target. It is the
// fixed remote end of scenery deploy, not an operator command.
func deployReceiveCommand(stdout io.Writer, args []string) error {
	if len(args) != 0 {
		return usageErrorf("deploy receive reads one request from stdin and takes no arguments")
	}
	paths, err := commandAgentPaths()
	if err != nil {
		return err
	}
	response := serveDeployRequest(context.Background(), paths.Home, io.LimitReader(configStdin, 64<<10), nil)
	return json.NewEncoder(stdout).Encode(response)
}

func serveDeployRequest(ctx context.Context, home string, input io.Reader, backendOverride func(*appconfig.Store) (appconfig.SecretBackend, error)) deployRemoteResponse {
	fail := func(code, format string, args ...any) deployRemoteResponse {
		return deployRemoteResponse{Kind: deployResponseKind, Protocol: deployProtocolVersion, Code: code, Error: fmt.Sprintf(format, args...)}
	}
	var request deployRemoteRequest
	decoder := json.NewDecoder(input)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Kind != deployRequestKind {
		return fail("invalid_request", "malformed deployment request")
	}
	if request.Protocol != deployProtocolVersion {
		return fail("capability_unavailable", "deployment protocol %d is not supported; this target speaks %d", request.Protocol, deployProtocolVersion)
	}
	if !appconfig.ValidIdentifier(request.AppID) || !appconfig.ValidIdentifier(request.Environment) || !configOperationIDPattern.MatchString(request.DeploymentID) {
		return fail("invalid_request", "deployment identity must be path-safe")
	}
	layout := deployLayout{home: home, appID: request.AppID, environment: request.Environment}
	store, err := appconfig.OpenStore(home, request.AppID)
	if err != nil {
		return fail("failed_precondition", "%v", err)
	}
	backend := func() (appconfig.SecretBackend, error) {
		if backendOverride != nil {
			return backendOverride(store)
		}
		return appconfig.DefaultSecretBackend(store)
	}
	var response deployRemoteResponse
	switch request.Operation {
	case "begin":
		response, err = layout.begin(store, request.DeploymentID)
	case "validate":
		response, err = layout.validate(ctx, store, request.DeploymentID, backend)
	case "abort":
		response, err = layout.abort(store, request.DeploymentID)
	case "activate":
		response, err = layout.activate(store, request.DeploymentID)
	case "commit":
		response, err = layout.commit(store, request.DeploymentID, backend)
	case "rollback":
		response, err = layout.rollback(store, request.DeploymentID)
	default:
		return fail("invalid_request", "unknown deployment operation")
	}
	if err != nil {
		if problems, ok := errors.AsType[*deployValidationError](err); ok {
			failed := fail("failed_precondition", "%s", err.Error())
			failed.Problems = problems.problems
			return failed
		}
		return fail("failed_precondition", "%s", humanCLIErrorMessage(err))
	}
	response.Kind, response.Protocol, response.OK = deployResponseKind, deployProtocolVersion, true
	response.SourceRoot = layout.sourceRoot()
	return response
}

// begin captures one desired configuration revision for this deployment and
// pins it, so later edits of desired configuration cannot change it.
func (layout deployLayout) begin(store *appconfig.Store, id string) (deployRemoteResponse, error) {
	if err := layout.legacyDeployRootError(); err != nil {
		return deployRemoteResponse{}, err
	}
	document, _, err := store.Read(layout.environment)
	if err != nil {
		return deployRemoteResponse{}, err
	}
	if err := os.MkdirAll(filepath.Join(layout.release(id), "source"), 0o700); err != nil {
		return deployRemoteResponse{}, err
	}
	if err := store.Pin(layout.environment, "deployment-"+id, document.Revision); err != nil {
		return deployRemoteResponse{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := layout.writeRelease(deployReleaseRecord{AppID: layout.appID, Environment: layout.environment, DeploymentID: id, State: "staging", ConfigRevision: document.Revision, CreatedAt: now}); err != nil {
		return deployRemoteResponse{}, err
	}
	response := deployRemoteResponse{ConfigRevision: document.Revision}
	relative, err := filepath.Rel(layout.home, filepath.Join(layout.release(id), "source"))
	if err != nil {
		return deployRemoteResponse{}, err
	}
	response.StagingPath = filepath.ToSlash(filepath.Join(".scenery", relative)) + "/"
	if filepath.Base(layout.home) != ".scenery" {
		response.StagingPath = filepath.ToSlash(filepath.Join(layout.release(id), "source")) + "/"
	}
	if active, err := readDeploymentActive(layout.home, layout.appID, layout.environment); err == nil && active != nil {
		response.Previous = active.DeploymentID
	}
	return response, nil
}

type deployValidationError struct{ problems []string }

func (err *deployValidationError) Error() string {
	return "the deployment candidate is not valid on the target: " + strings.Join(err.problems, "; ")
}

// validate compiles the staged source on the target and resolves the pinned
// configuration revision against its catalog, including every secret, while
// the healthy runtime keeps serving.
func (layout deployLayout) validate(ctx context.Context, store *appconfig.Store, id string, backend func() (appconfig.SecretBackend, error)) (deployRemoteResponse, error) {
	record, err := layout.readRelease(id)
	if err != nil {
		return deployRemoteResponse{}, err
	}
	staged := filepath.Join(layout.release(id), "source")
	root, cfg, err := app.DiscoverRoot(staged)
	canonical, canonicalErr := filepath.EvalSymlinks(staged)
	if err != nil || canonicalErr != nil || (filepath.Clean(root) != filepath.Clean(staged) && filepath.Clean(root) != filepath.Clean(canonical)) {
		return deployRemoteResponse{}, fmt.Errorf("staged source has no application root of its own: %v", errors.Join(err, canonicalErr))
	}
	if cfg.ID != layout.appID {
		return deployRemoteResponse{}, &deployValidationError{problems: []string{fmt.Sprintf("staged application id %q does not match %q", cfg.ID, layout.appID)}}
	}
	env, err := cfg.ResolveEnv(layout.environment)
	if err != nil || !env.Deployable() {
		return deployRemoteResponse{}, &deployValidationError{problems: []string{"environment " + layout.environment + " is not a deployable environment of the staged application"}}
	}
	catalog, err := compileConfigCatalog(staged, cfg)
	if err != nil {
		return deployRemoteResponse{}, &deployValidationError{problems: []string{humanCLIErrorMessage(err)}}
	}
	document, err := store.ReadRevision(layout.environment, record.ConfigRevision)
	if err != nil {
		return deployRemoteResponse{}, err
	}
	resolution := appconfig.Resolve(catalog, document, true)
	var problems []string
	for _, entry := range resolution.Entries {
		switch {
		case entry.State == appconfig.StateMissing || entry.State == appconfig.StateInvalid:
			problems = append(problems, entry.Key+": "+entry.Problem)
		case entry.Secret != nil:
			secrets, err := backend()
			if err == nil {
				_, err = secrets.Resolve(ctx, layout.environment, entry.Key, *entry.Secret)
			}
			if err != nil {
				problems = append(problems, entry.Key+": secret is not readable by the runtime owner: "+err.Error())
			}
		}
	}
	if len(problems) > 0 {
		return deployRemoteResponse{}, &deployValidationError{problems: problems}
	}
	record.State, record.CatalogRevision = "validated", catalog.Revision
	if err := layout.writeRelease(*record); err != nil {
		return deployRemoteResponse{}, err
	}
	return deployRemoteResponse{ConfigRevision: record.ConfigRevision}, nil
}

// abort drops a release that never touched the runtime.
func (layout deployLayout) abort(store *appconfig.Store, id string) (deployRemoteResponse, error) {
	if record, err := layout.readRelease(id); err == nil && record.State != "active" {
		record.State = "aborted"
		_ = layout.writeRelease(*record)
		_ = os.RemoveAll(filepath.Join(layout.release(id), "source"))
	}
	return deployRemoteResponse{}, store.Unpin(layout.environment, "deployment-"+id)
}

// activate installs the validated release into the stable root and records
// it as the installed release with its pinned configuration. The caller has
// stopped the runtime; a restart, reboot or resume from here on runs this
// exact source and configuration pair until commit or rollback.
func (layout deployLayout) activate(store *appconfig.Store, id string) (deployRemoteResponse, error) {
	record, err := layout.readRelease(id)
	if err != nil {
		return deployRemoteResponse{}, err
	}
	if record.State != "validated" {
		return deployRemoteResponse{}, fmt.Errorf("release %s is %s, not validated", id, record.State)
	}
	previous, err := readDeploymentActive(layout.home, layout.appID, layout.environment)
	if err != nil {
		return deployRemoteResponse{}, err
	}
	if err := syncReleaseSource(filepath.Join(layout.release(id), "source"), layout.sourceRoot()); err != nil {
		return deployRemoteResponse{}, err
	}
	active := deploymentActiveRecord{DeploymentID: id, State: "activating", ConfigRevision: record.ConfigRevision, CatalogRevision: record.CatalogRevision, SourceRoot: layout.sourceRoot(), ActivatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if previous != nil {
		active.Previous = previous.DeploymentID
	}
	if err := store.Pin(layout.environment, "deployment-active", record.ConfigRevision); err != nil {
		return deployRemoteResponse{}, err
	}
	if err := layout.writeActive(active); err != nil {
		return deployRemoteResponse{}, err
	}
	record.State = "activating"
	return deployRemoteResponse{ConfigRevision: record.ConfigRevision, Previous: active.Previous}, layout.writeRelease(*record)
}

// commit confirms an activated release after readiness and publication.
func (layout deployLayout) commit(store *appconfig.Store, id string, backend func() (appconfig.SecretBackend, error)) (deployRemoteResponse, error) {
	active, err := readDeploymentActive(layout.home, layout.appID, layout.environment)
	if err != nil || active == nil || active.DeploymentID != id {
		return deployRemoteResponse{}, fmt.Errorf("release %s is not the installed release", id)
	}
	if active.Previous != "" {
		if previous, err := layout.readRelease(active.Previous); err == nil {
			if err := store.Pin(layout.environment, "deployment-rollback", previous.ConfigRevision); err != nil {
				return deployRemoteResponse{}, err
			}
			previous.State = "rollback"
			_ = layout.writeRelease(*previous)
		}
	}
	active.State = "active"
	if err := layout.writeActive(*active); err != nil {
		return deployRemoteResponse{}, err
	}
	record, err := layout.readRelease(id)
	if err != nil {
		return deployRemoteResponse{}, err
	}
	record.State = "active"
	if err := layout.writeRelease(*record); err != nil {
		return deployRemoteResponse{}, err
	}
	_ = store.Unpin(layout.environment, "deployment-"+id)
	layout.pruneReleases(id, active.Previous)
	pruneConfigHistory(context.Background(), store, layout.environment, backend)
	return deployRemoteResponse{ConfigRevision: active.ConfigRevision, Previous: active.Previous}, nil
}

// rollback reinstalls the previous release's exact source and configuration
// after a failed activation. The caller has stopped the failed runtime.
func (layout deployLayout) rollback(store *appconfig.Store, id string) (deployRemoteResponse, error) {
	active, err := readDeploymentActive(layout.home, layout.appID, layout.environment)
	if err != nil {
		return deployRemoteResponse{}, err
	}
	if record, err := layout.readRelease(id); err == nil {
		record.State = "failed"
		_ = layout.writeRelease(*record)
	}
	defer func() { _ = store.Unpin(layout.environment, "deployment-"+id) }()
	if active == nil || active.DeploymentID != id {
		return deployRemoteResponse{}, nil
	}
	if active.Previous == "" {
		// The first release has nothing to restore; the environment stays
		// stopped without an installed release.
		_ = store.Unpin(layout.environment, "deployment-active")
		return deployRemoteResponse{}, os.Remove(filepath.Join(layout.state(), "active.json"))
	}
	previous, err := layout.readRelease(active.Previous)
	if err != nil {
		return deployRemoteResponse{}, fmt.Errorf("previous release %s is not retained: %w", active.Previous, err)
	}
	if err := syncReleaseSource(filepath.Join(layout.release(previous.DeploymentID), "source"), layout.sourceRoot()); err != nil {
		return deployRemoteResponse{}, err
	}
	restored := deploymentActiveRecord{DeploymentID: previous.DeploymentID, State: "active", ConfigRevision: previous.ConfigRevision, CatalogRevision: previous.CatalogRevision, SourceRoot: layout.sourceRoot(), ActivatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := store.Pin(layout.environment, "deployment-active", previous.ConfigRevision); err != nil {
		return deployRemoteResponse{}, err
	}
	previous.State = "active"
	_ = layout.writeRelease(*previous)
	return deployRemoteResponse{ConfigRevision: previous.ConfigRevision, Previous: previous.DeploymentID}, layout.writeActive(restored)
}

// pruneReleases keeps the installed and rollback releases' sources.
func (layout deployLayout) pruneReleases(keep ...string) {
	entries, err := os.ReadDir(filepath.Join(layout.state(), "releases"))
	if err != nil {
		return
	}
	kept := map[string]bool{}
	for _, id := range keep {
		kept[id] = true
	}
	var stale []string
	for _, entry := range entries {
		if !kept[entry.Name()] && configOperationIDPattern.MatchString(entry.Name()) {
			stale = append(stale, entry.Name())
		}
	}
	sort.Strings(stale)
	for _, id := range stale {
		// Receipts stay as history; only source trees are removed.
		_ = os.RemoveAll(filepath.Join(layout.release(id), "source"))
	}
	_ = retainedDeployReleases
}

// syncReleaseSource makes target an exact copy of source, except the
// runtime-owned .scenery directory, which keeps the root's build caches and
// session state across releases.
func syncReleaseSource(source, target string) error {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	wanted := map[string]bool{}
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(source, path)
		if relative == "." {
			return nil
		}
		if relative == ".scenery" || strings.HasPrefix(relative, ".scenery"+string(filepath.Separator)) {
			return filepath.SkipDir
		}
		wanted[relative] = true
		destination := filepath.Join(target, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			return os.MkdirAll(destination, info.Mode().Perm()|0o700)
		case entry.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_ = os.RemoveAll(destination)
			return os.Symlink(link, destination)
		default:
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if existing, err := os.ReadFile(destination); err == nil && bytes.Equal(existing, data) {
				return os.Chmod(destination, info.Mode().Perm())
			}
			_ = os.RemoveAll(destination)
			// Source files need no flush: active.json, written after the
			// copy, is the durable commit point of an installation.
			return atomicfile.Write(destination, data, info.Mode().Perm(), atomicfile.Options{})
		}
	})
	if err != nil {
		return err
	}
	var stale []string
	err = filepath.WalkDir(target, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(target, path)
		if relative == "." {
			return nil
		}
		if relative == ".scenery" {
			return filepath.SkipDir
		}
		if !wanted[relative] {
			stale = append(stale, path)
			if entry.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, path := range stale {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}
