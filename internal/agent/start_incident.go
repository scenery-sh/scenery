package agent

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"scenery.sh/internal/machine"
	"scenery.sh/internal/redact"
)

// Start failure classes. A persistent class keeps failing until something
// outside the agent changes, so a supervised agent blocks at once; a transient
// class is retried with growing delays and blocks after a bounded number of
// attempts. A blocked supervised agent stays alive idle, holding no lock,
// address or socket, so its supervisor (launchd KeepAlive, systemd
// Restart=always) has nothing to restart until an explicit restart.
const (
	// StartClassState: durable agent state cannot be read, for example an
	// artifact written by another Scenery version.
	StartClassState = "state"
	// StartClassOwner: another agent process holds this home's agent lock.
	StartClassOwner = "owner"
	// StartClassConfiguration: the requested socket or address can never be
	// used as given, such as a socket path longer than the platform allows.
	StartClassConfiguration = "configuration"
	// StartClassUnavailable: an address or socket the agent listens on is
	// unavailable.
	StartClassUnavailable = "unavailable"
	// StartClassOther: any other start failure.
	StartClassOther = "other"
)

const (
	agentStartIncidentKind             = "scenery.agent.start-incident"
	agentStartIncidentSchemaDescriptor = `{"identity":"artifact","incident":"agent-start"}`
	// startTransientAttemptLimit bounds the retries of one transient
	// incident before the agent is blocked like a persistent one.
	startTransientAttemptLimit = 8
	startRetryDelayCap         = time.Minute
	startIncidentCauseLimit    = 600
)

// StartFailure classifies an error that kept the agent from starting.
type StartFailure struct {
	Class string
	Err   error
}

func (f *StartFailure) Error() string { return f.Err.Error() }
func (f *StartFailure) Unwrap() error { return f.Err }

func startFailure(class string, err error) error {
	if err == nil {
		return nil
	}
	return &StartFailure{Class: class, Err: err}
}

// StartFailureClass returns the class of a start error; an unclassified error
// is StartClassOther.
func StartFailureClass(err error) string {
	var failure *StartFailure
	if errors.As(err, &failure) {
		return failure.Class
	}
	return StartClassOther
}

func persistentStartClass(class string) bool {
	return class == StartClassState || class == StartClassOwner || class == StartClassConfiguration
}

// StartIncident is one run of identical agent start failures: the same
// executable failing for the same cause. It survives the failed processes, so
// every restart adds to one incident instead of starting over.
type StartIncident struct {
	machine.ArtifactIdentity
	// State is "retrying" while a supervisor should start the agent again, and
	// "blocked" once further automatic starts cannot succeed.
	State string `json:"state"`
	Class string `json:"class"`
	Cause string `json:"cause"`
	// Executable identifies the agent executable that failed; the artifact
	// identity's producer names the Scenery build that wrote the record.
	Executable string    `json:"executable"`
	Attempts   int       `json:"attempts"`
	FirstAt    time.Time `json:"first_at"`
	LastAt     time.Time `json:"last_at"`
	NextDelay  string    `json:"next_delay,omitempty"`
}

// StartDecision tells a supervised agent what to do after a failed start.
type StartDecision struct {
	Incident StartIncident
	// Blocked means the agent must stay alive idle so its supervisor stops
	// restarting it; otherwise it waits Delay and exits with the failure.
	Blocked bool
	Delay   time.Duration
}

func agentStartIncidentIdentity() machine.ArtifactIdentity {
	return machine.NewArtifactIdentity(agentStartIncidentKind, agentStartIncidentSchemaDescriptor)
}

// RecordStartFailure adds a failed start to the current incident, or begins a
// new one when the executable or cause differs, and decides whether a
// supervisor may start the agent again.
func RecordStartFailure(paths Paths, executable string, err error, now time.Time) (StartDecision, error) {
	class := StartFailureClass(err)
	cause := redact.String(strings.TrimSpace(err.Error()))
	if len(cause) > startIncidentCauseLimit {
		cause = cause[:startIncidentCauseLimit]
	}
	incident, loadErr := LoadStartIncident(paths)
	if loadErr != nil || incident.Executable != executable || incident.Class != class || incident.Cause != cause {
		incident = StartIncident{Class: class, Cause: cause, Executable: executable, FirstAt: now.UTC()}
	}
	incident.ArtifactIdentity = agentStartIncidentIdentity()
	incident.Attempts++
	incident.LastAt = now.UTC()
	decision := StartDecision{}
	if persistentStartClass(class) || incident.Attempts >= startTransientAttemptLimit {
		decision.Blocked = true
		incident.State = "blocked"
		incident.NextDelay = ""
	} else {
		decision.Delay = time.Second << (incident.Attempts - 1)
		if decision.Delay > startRetryDelayCap {
			decision.Delay = startRetryDelayCap
		}
		incident.State = "retrying"
		incident.NextDelay = decision.Delay.String()
	}
	decision.Incident = incident
	data, marshalErr := json.MarshalIndent(incident, "", "  ")
	if marshalErr != nil {
		return decision, marshalErr
	}
	if err := os.MkdirAll(paths.RunDir, 0o700); err != nil {
		return decision, err
	}
	return decision, atomicWriteFile(paths.AgentStartIncidentPath, append(data, '\n'), 0o600)
}

// LoadStartIncident reads the current start incident; os.ErrNotExist means the
// agent's last start succeeded or none failed.
func LoadStartIncident(paths Paths) (StartIncident, error) {
	if paths.AgentStartIncidentPath == "" {
		return StartIncident{}, os.ErrNotExist
	}
	data, err := os.ReadFile(paths.AgentStartIncidentPath)
	if err != nil {
		return StartIncident{}, err
	}
	var incident StartIncident
	if err := machine.DecodeArtifact(data, &incident, &incident.ArtifactIdentity, agentStartIncidentKind, agentStartIncidentSchemaDescriptor, "restart the scenery agent"); err != nil {
		return StartIncident{}, err
	}
	return incident, nil
}

// ClearStartIncident ends the incident once the agent started.
func ClearStartIncident(paths Paths) error {
	if paths.AgentStartIncidentPath == "" {
		return nil
	}
	if err := os.Remove(paths.AgentStartIncidentPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
