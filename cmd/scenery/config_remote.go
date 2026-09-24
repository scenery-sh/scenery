package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/appconfig"
)

// The configuration receiver is Scenery's private, versioned boundary for
// reading and mutating a deployable environment's desired configuration on
// its target. Requests arrive as one bounded JSON object on stdin of a fixed
// remote command; nothing from the request is ever interpolated into a
// shell command.
const (
	configRequestKind     = "scenery.config.request"
	configResponseKind    = "scenery.config.response"
	configProtocolVersion = 1
	maxConfigRequestBytes = 4 * appconfig.MaxSecretBytes
	// configRemoteCommand is the fixed command the SSH session runs.
	configRemoteCommand = "scenery config receive"
)

var configOperationIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type configRemoteRequest struct {
	Kind           string          `json:"kind"`
	Protocol       int             `json:"protocol"`
	AppID          string          `json:"app_id"`
	Environment    string          `json:"environment"`
	Operation      string          `json:"operation"`
	OperationID    string          `json:"operation_id,omitempty"`
	Key            string          `json:"key,omitempty"`
	Value          json.RawMessage `json:"value,omitempty"`
	Secret         []byte          `json:"secret,omitempty"`
	ExpectRevision string          `json:"expect_revision,omitempty"`
}

// configTargetActive is the configuration the target's active deployment
// runs; it is distinct from the caller's catalog.
type configTargetActive struct {
	Revision        string `json:"revision"`
	CatalogRevision string `json:"catalog_revision"`
	DeploymentID    string `json:"deployment_id"`
}

type configRemoteResponse struct {
	Kind     string                     `json:"kind"`
	Protocol int                        `json:"protocol"`
	OK       bool                       `json:"ok"`
	Code     string                     `json:"code,omitempty"`
	Error    string                     `json:"error,omitempty"`
	Revision string                     `json:"revision,omitempty"`
	Values   map[string]json.RawMessage `json:"values,omitempty"`
	// Secrets names configured secret keys; no version or material leaves
	// the target.
	Secrets []string                  `json:"secrets,omitempty"`
	Result  *appconfig.MutationResult `json:"result,omitempty"`
	Active  *configTargetActive       `json:"active,omitempty"`
}

// document rebuilds a caller-side view of the target document for
// resolution. Secret references are placeholders: the caller only learns
// whether a secret is configured.
func (response configRemoteResponse) document(appID, environment string) appconfig.Document {
	document := appconfig.NewDocument(appID, environment)
	for key, value := range response.Values {
		document.Values[key] = value
	}
	for _, key := range response.Secrets {
		document.Secrets[key] = appconfig.SecretVersion{Backend: "target", Version: strings.Repeat("0", 32)}
	}
	document.Revision = response.Revision
	return document
}

// configRemoteTransport carries one request to a target and returns its
// response bytes.
type configRemoteTransport interface {
	Exchange(ctx context.Context, target string, request []byte) ([]byte, error)
}

type sshConfigTransport struct{ tools deploySSHTools }

func (t sshConfigTransport) Exchange(ctx context.Context, target string, request []byte) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	command := exec.CommandContext(ctx, t.tools.ssh(), "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "--", target, configRemoteCommand)
	command.Stdin = bytes.NewReader(request)
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		// stderr can carry remote diagnostics but never request material:
		// the receiver writes its failures as a JSON response.
		if stdout.Len() > 0 {
			return stdout.Bytes(), nil
		}
		return nil, unavailableErrorf("configuration target %s is unreachable over SSH: %v; no local value was used", target, err)
	}
	return stdout.Bytes(), nil
}

func (s *configSession) exchange(ctx context.Context, request configRemoteRequest) (configRemoteResponse, error) {
	request.Kind, request.Protocol = configRequestKind, configProtocolVersion
	request.AppID, request.Environment = s.cfg.ID, s.env.Name
	if request.Operation != "read" {
		identifier := make([]byte, 16)
		if _, err := rand.Read(identifier); err != nil {
			return configRemoteResponse{}, err
		}
		request.OperationID = hex.EncodeToString(identifier)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return configRemoteResponse{}, err
	}
	var transport configRemoteTransport = sshConfigTransport{}
	if configRemoteTransportOverride != nil {
		transport = configRemoteTransportOverride
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	raw, err := transport.Exchange(ctx, s.target, encoded)
	if err != nil {
		return configRemoteResponse{}, err
	}
	var response configRemoteResponse
	decoder := json.NewDecoder(io.LimitReader(bytes.NewReader(raw), maxConfigRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil || response.Kind != configResponseKind {
		return configRemoteResponse{}, unavailableErrorf("configuration target %s did not answer with a Scenery configuration response; upgrade Scenery on the target", s.target)
	}
	if response.Protocol != configProtocolVersion {
		return configRemoteResponse{}, unavailableErrorf("configuration target %s speaks protocol %d, this Scenery speaks %d; use matching Scenery versions", s.target, response.Protocol, configProtocolVersion)
	}
	if !response.OK {
		switch response.Code {
		case "revision_conflict":
			return response, &codedCLIError{err: fmt.Errorf("revision_conflict: target %s: %s", s.target, response.Error), code: 3}
		case "invalid_request":
			return response, usageErrorf("target %s: %s", s.target, response.Error)
		case "capability_unavailable":
			return response, unavailableErrorf("target %s: %s", s.target, response.Error)
		default:
			return response, preconditionErrorf("target %s: %s", s.target, response.Error)
		}
	}
	if request.Operation != "read" && response.Result == nil {
		return response, unavailableErrorf("configuration target %s answered a mutation without its result", s.target)
	}
	return response, nil
}

// configReceiveCommand serves one request against this host's store. It is
// the fixed remote end of the SSH transport and is not an operator command.
func configReceiveCommand(stdout io.Writer, args []string) error {
	if len(args) != 0 {
		return usageErrorf("config receive reads one request from stdin and takes no arguments")
	}
	paths, err := commandAgentPaths()
	if err != nil {
		return err
	}
	response := serveConfigRequest(context.Background(), paths.Home, io.LimitReader(configStdin, maxConfigRequestBytes+1), nil)
	return json.NewEncoder(stdout).Encode(response)
}

// serveConfigRequest validates and applies one request. Failures become
// responses; none echoes request values.
func serveConfigRequest(ctx context.Context, home string, input io.Reader, backendOverride func(*appconfig.Store) (appconfig.SecretBackend, error)) configRemoteResponse {
	fail := func(code, format string, args ...any) configRemoteResponse {
		return configRemoteResponse{Kind: configResponseKind, Protocol: configProtocolVersion, Code: code, Error: fmt.Sprintf(format, args...)}
	}
	data, err := io.ReadAll(input)
	if err != nil || len(data) > maxConfigRequestBytes {
		return fail("invalid_request", "configuration request exceeds %d bytes or cannot be read", maxConfigRequestBytes)
	}
	var request configRemoteRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Kind != configRequestKind {
		return fail("invalid_request", "malformed configuration request")
	}
	if request.Protocol != configProtocolVersion {
		return fail("capability_unavailable", "protocol %d is not supported; this target speaks %d", request.Protocol, configProtocolVersion)
	}
	if !appconfig.ValidIdentifier(request.AppID) || !appconfig.ValidIdentifier(request.Environment) {
		return fail("invalid_request", "application id and environment must be path-safe identifiers")
	}
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
	switch request.Operation {
	case "read":
	case "set", "unset":
		if !configOperationIDPattern.MatchString(request.OperationID) {
			return fail("invalid_request", "mutation requires an operation id")
		}
		if replay, ok := readConfigOperation(store, request.Environment, request.OperationID); ok {
			return replay
		}
		if request.Operation == "set" && (request.Value == nil) == (request.Secret == nil) {
			return fail("invalid_request", "set carries exactly one value or secret")
		}
		if request.Operation == "unset" && (request.Value != nil || request.Secret != nil) {
			return fail("invalid_request", "unset carries no value")
		}
		result, err := applyLocalConfigMutation(ctx, store, request.Environment, request, backend)
		if err != nil {
			code := "failed_precondition"
			switch cliExitCode(err) {
			case 2:
				code = "invalid_request"
			case 4:
				code = "capability_unavailable"
			}
			if errors.Is(err, appconfig.ErrRevisionConflict) {
				code = "revision_conflict"
			}
			return fail(code, "%s", humanCLIErrorMessage(err))
		}
		response := configRemoteResponse{Kind: configResponseKind, Protocol: configProtocolVersion, OK: true, Result: &result}
		finished := describeConfigTarget(store, request.Environment, response)
		recordConfigOperation(store, request.Environment, request.OperationID, finished)
		return finished
	default:
		return fail("invalid_request", "unknown configuration operation")
	}
	return describeConfigTarget(store, request.Environment, configRemoteResponse{Kind: configResponseKind, Protocol: configProtocolVersion, OK: true})
}

// describeConfigTarget adds the desired document's non-secret values, the
// names of configured secrets and the active deployment's pin.
func describeConfigTarget(store *appconfig.Store, environment string, response configRemoteResponse) configRemoteResponse {
	document, _, err := store.Read(environment)
	if err != nil {
		return configRemoteResponse{Kind: configResponseKind, Protocol: configProtocolVersion, Code: "failed_precondition", Error: fmt.Sprintf("read %s configuration: %v", environment, err)}
	}
	response.Revision, response.Values, response.Secrets = document.Revision, document.Values, []string{}
	for key := range document.Secrets {
		response.Secrets = append(response.Secrets, key)
	}
	sort.Strings(response.Secrets)
	if active, err := readDeploymentActive(filepath.Dir(filepath.Dir(store.Dir())), store.AppID(), environment); err == nil && active != nil {
		response.Active = &configTargetActive{Revision: active.ConfigRevision, CatalogRevision: active.CatalogRevision, DeploymentID: active.DeploymentID}
	}
	return response
}

func configOperationPath(store *appconfig.Store, environment, operation string) string {
	return filepath.Join(store.Dir(), "environment-history", environment, "operations", operation+".json")
}

// readConfigOperation returns the recorded response of an already applied
// mutation, so a retried request never replays over a newer value.
func readConfigOperation(store *appconfig.Store, environment, operation string) (configRemoteResponse, bool) {
	data, err := os.ReadFile(configOperationPath(store, environment, operation))
	if err != nil {
		return configRemoteResponse{}, false
	}
	var response configRemoteResponse
	if json.Unmarshal(data, &response) != nil || response.Kind != configResponseKind {
		return configRemoteResponse{}, false
	}
	return response, true
}

// recordConfigOperation remembers a mutation response and keeps the newest
// 64 records.
func recordConfigOperation(store *appconfig.Store, environment, operation string, response configRemoteResponse) {
	path := configOperationPath(store, environment, operation)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	encoded, err := json.Marshal(response)
	if err != nil || atomicWriteFile(path, encoded, 0o600) != nil {
		return
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) <= 64 {
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		left, _ := entries[i].Info()
		right, _ := entries[j].Info()
		return left != nil && right != nil && left.ModTime().Before(right.ModTime())
	})
	for _, entry := range entries[:len(entries)-64] {
		_ = os.Remove(filepath.Join(filepath.Dir(path), entry.Name()))
	}
}
