package agent

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// AgentLaunchdLabel is the launchd user-agent job that continuously
// supervises the scenery agent control plane and router. It is the required
// availability owner for machines that serve public deploy traffic: launchd
// restarts the agent when it exits, and every scenery stop/start path must
// cooperate with that supervisor instead of racing its KeepAlive respawn.
const AgentLaunchdLabel = "dev.scenery.agent"

// launchdBootstrapRetryWindow bounds retries of launchctl bootstrap after a
// bootout: launchctl bootout returns before launchd has finished tearing the
// old job down, so an immediate bootstrap of the replacement can fail
// transiently with EIO.
const launchdBootstrapRetryWindow = 10 * time.Second

var (
	launchctlRunFunc     = runLaunchctl
	launchAgentsDirFunc  = defaultLaunchAgentsDir
	launchdSleepFunc     = time.Sleep
	launchdUserIDFunc    = os.Getuid
	launchdSupportedFunc = func() bool { return runtime.GOOS == "darwin" }
)

func runLaunchctl(args ...string) ([]byte, error) {
	return exec.Command("launchctl", args...).CombinedOutput()
}

func defaultLaunchAgentsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents"), nil
}

// LaunchdAgentStatus reports launchd supervision truth for the scenery agent:
// plist presence is installation, Loaded/Running come from launchd itself.
// Presence of the plist file alone never means the agent is supervised.
type LaunchdAgentStatus struct {
	Supported        bool   `json:"supported"`
	PlistPresent     bool   `json:"installed"`
	SupervisesSocket bool   `json:"supervises_socket"`
	Loaded           bool   `json:"loaded"`
	Running          bool   `json:"running"`
	PID              int    `json:"pid,omitempty"`
	PlistPath        string `json:"path"`
	Label            string `json:"label"`
}

func launchdGUITarget() string {
	return fmt.Sprintf("gui/%d/%s", launchdUserIDFunc(), AgentLaunchdLabel)
}

// AgentLaunchdPlistPath resolves the supervised agent plist path without
// checking whether it exists.
func AgentLaunchdPlistPath() (string, error) {
	dir, err := launchAgentsDirFunc()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, AgentLaunchdLabel+".plist"), nil
}

// AgentLaunchdPlist renders the supervised agent job. KeepAlive keeps the
// agent continuously owned by launchd; a supervised agent whose start cannot
// succeed stays alive idle instead of exiting, so launchd has nothing to
// restart until `scenery system agent restart` requests another start; RunAtLoad starts it at bootstrap and
// at every login. launchd's default PATH is only the system directories, so
// the job pins a PATH that includes the standard Homebrew and local prefixes
// — the agent's dashboard shells out to tools like docker (managed Postgres)
// that live there.
func AgentLaunchdPlist(exe string, paths Paths, opts StartOptions) string {
	opts.Supervised = true
	args := append([]string{exe}, agentProcessArgs(paths, opts)...)
	var argLines strings.Builder
	for _, arg := range args {
		fmt.Fprintf(&argLines, "\t\t<string>%s</string>\n", plistEscape(arg))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
%s
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>ThrottleInterval</key>
	<integer>2</integer>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
	</dict>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, plistEscape(AgentLaunchdLabel), strings.TrimRight(argLines.String(), "\n"), plistEscape(paths.LogPath), plistEscape(paths.LogPath))
}

func plistEscape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(value)
}

// InstallAgentLaunchd writes the supervised agent plist and actually loads it:
// installation means a bootstrapped launchd job, not a plist file on disk.
// RunAtLoad starts the agent immediately, so callers must stop any
// unsupervised agent that still holds the agent lock before installing.
func InstallAgentLaunchd(exe string, paths Paths, opts StartOptions) (string, error) {
	if !launchdSupportedFunc() {
		return "", fmt.Errorf("scenery agent launchd supervision is currently supported on macOS")
	}
	plistPath, err := AgentLaunchdPlistPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(plistPath, []byte(AgentLaunchdPlist(exe, paths, opts)), 0o644); err != nil {
		return "", err
	}
	_, _ = launchctlRunFunc("bootout", launchdGUITarget())
	if err := bootstrapAndStartAgentLaunchd(plistPath); err != nil {
		return "", err
	}
	return plistPath, nil
}

// bootstrapAndStartAgentLaunchd loads the job and then starts it explicitly:
// launchd can pend a RunAtLoad spawn when the job is bootstrapped from a
// non-Aqua context (observed as "pended nondemand spawn = speculative"), so
// bootstrap alone does not guarantee a running agent.
func bootstrapAndStartAgentLaunchd(plistPath string) error {
	if err := retryLaunchctl(launchdBootstrapRetryWindow, "bootstrap", fmt.Sprintf("gui/%d", launchdUserIDFunc()), plistPath); err != nil {
		return err
	}
	if err := KickstartAgentLaunchd(false); err != nil {
		if AgentLaunchdStatusForSocket("").Running {
			return nil
		}
		return err
	}
	return nil
}

// BootstrapAgentLaunchd loads an already-installed supervised agent plist.
// It repairs the "plist present but job unloaded" state without rewriting
// the plist.
func BootstrapAgentLaunchd() error {
	if !launchdSupportedFunc() {
		return fmt.Errorf("scenery agent launchd supervision is currently supported on macOS")
	}
	plistPath, err := AgentLaunchdPlistPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(plistPath); err != nil {
		return err
	}
	return bootstrapAndStartAgentLaunchd(plistPath)
}

// ReloadAgentLaunchd re-registers the loaded supervised agent job: bootout
// stops the running agent, then bootstrap and an explicit start load the
// installed plist again. Registration is when launchd records the job's
// launch constraints, including the executable's code-directory hash, so a
// kickstart of the old registration refuses a reinstalled binary at the same
// path ("needs LWCR update", spawn failed with EX_CONFIG). This is the form of
// `scenery system agent restart` under supervision.
func ReloadAgentLaunchd() error {
	if !launchdSupportedFunc() {
		return fmt.Errorf("scenery agent launchd supervision is currently supported on macOS")
	}
	plistPath, err := AgentLaunchdPlistPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(plistPath); err != nil {
		return err
	}
	// An already unloaded job is the state bootout produces; bootstrap below
	// reports any real launchd failure.
	_, _ = launchctlRunFunc("bootout", launchdGUITarget())
	return bootstrapAndStartAgentLaunchd(plistPath)
}

// ReconcileAgentLaunchd rewrites the installed supervised agent plist from the
// current template, keeping the executable, socket, router and log settings
// it names, and reports whether it changed. A plist written by an earlier
// Scenery can lack an invocation the current agent relies on, such as
// --supervised, which enables start failure containment; registering such a
// job again unchanged would keep it missing. Call it before the job is
// registered again; a plist Scenery cannot read is left untouched.
func ReconcileAgentLaunchd() (bool, error) {
	plistPath, err := AgentLaunchdPlistPath()
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(plistPath)
	if err != nil {
		return false, err
	}
	exe, paths, opts, err := parseAgentLaunchdPlist(data)
	if err != nil {
		return false, fmt.Errorf("the installed supervised agent plist %s is not one Scenery renders (%w); reinstall it with scenery deploy setup", plistPath, err)
	}
	rendered := AgentLaunchdPlist(exe, paths, opts)
	if rendered == string(data) {
		return false, nil
	}
	if err := atomicWriteFile(plistPath, []byte(rendered), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// parseAgentLaunchdPlist reads the agent invocation and log path of a
// supervised agent plist.
func parseAgentLaunchdPlist(data []byte) (string, Paths, StartOptions, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true
	var args []string
	var logPath string
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", Paths{}, StartOptions{}, err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "key" {
			continue
		}
		// Each key of a dictionary is followed by its value, which is
		// consumed whole, so keys of nested dictionaries are never read.
		var key string
		if err := decoder.DecodeElement(&key, &start); err != nil {
			return "", Paths{}, StartOptions{}, err
		}
		value, err := nextStartElement(decoder)
		if err != nil {
			return "", Paths{}, StartOptions{}, err
		}
		switch {
		case key == "ProgramArguments" && value.Name.Local == "array":
			var array struct {
				Strings []string `xml:"string"`
			}
			if err := decoder.DecodeElement(&array, &value); err != nil {
				return "", Paths{}, StartOptions{}, err
			}
			args = array.Strings
		case key == "StandardOutPath" && value.Name.Local == "string":
			if err := decoder.DecodeElement(&logPath, &value); err != nil {
				return "", Paths{}, StartOptions{}, err
			}
		default:
			if err := decoder.Skip(); err != nil {
				return "", Paths{}, StartOptions{}, err
			}
		}
	}
	exe, socketPath, opts, err := parseAgentInvocation(args)
	if err != nil {
		return "", Paths{}, StartOptions{}, err
	}
	if logPath == "" {
		return "", Paths{}, StartOptions{}, errors.New("no StandardOutPath")
	}
	return exe, Paths{SocketPath: socketPath, LogPath: logPath}, opts, nil
}

func nextStartElement(decoder *xml.Decoder) (xml.StartElement, error) {
	for {
		token, err := decoder.Token()
		if err != nil {
			return xml.StartElement{}, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			return token, nil
		case xml.EndElement:
			return xml.StartElement{}, fmt.Errorf("key without a value before </%s>", token.Name.Local)
		}
	}
}

// parseAgentInvocation reads an agent invocation that agentProcessArgs
// renders, preceded by its executable.
func parseAgentInvocation(args []string) (string, string, StartOptions, error) {
	if len(args) < 3 || args[1] != "system" || args[2] != "agent" {
		return "", "", StartOptions{}, fmt.Errorf("the job does not run `scenery system agent`: %q", args)
	}
	var socketPath string
	var opts StartOptions
	for index := 3; index < len(args); index++ {
		switch arg := args[index]; arg {
		case "--socket", "--router-listen":
			if index+1 >= len(args) {
				return "", "", StartOptions{}, fmt.Errorf("%s has no value", arg)
			}
			index++
			if arg == "--socket" {
				socketPath = args[index]
			} else {
				opts.RouterAddr = args[index]
			}
		case "--router-http":
			opts.RouterHTTP = true
		case "--router-tls":
			opts.RouterTLS = true
		case "--trust":
			opts.Trust = true
		case "--supervised":
			opts.Supervised = true
		default:
			return "", "", StartOptions{}, fmt.Errorf("unknown agent argument %q", arg)
		}
	}
	if args[0] == "" || socketPath == "" || opts.RouterAddr == "" {
		return "", "", StartOptions{}, errors.New("the job names no executable, --socket or --router-listen")
	}
	return args[0], socketPath, opts, nil
}

// RemoveAgentLaunchd boots the supervised agent job out of launchd before
// removing its plist, so teardown never leaves a loaded job pointing at a
// deleted plist.
func RemoveAgentLaunchd() (bool, error) {
	plistPath, err := AgentLaunchdPlistPath()
	if err != nil {
		return false, err
	}
	if launchdSupportedFunc() {
		_, _ = launchctlRunFunc("bootout", launchdGUITarget())
	}
	err = os.Remove(plistPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// KickstartAgentLaunchd asks launchd to (re)start the supervised agent. With
// kill=true the running agent is terminated and respawned under the existing
// registration; use ReloadAgentLaunchd when the executable may have changed.
func KickstartAgentLaunchd(kill bool) error {
	args := []string{"kickstart"}
	if kill {
		args = append(args, "-k")
	}
	args = append(args, launchdGUITarget())
	out, err := launchctlRunFunc(args...)
	if err != nil {
		return fmt.Errorf("launchctl kickstart %s: %w: %s", launchdGUITarget(), err, strings.TrimSpace(string(out)))
	}
	return nil
}

var launchdPrintPIDRE = regexp.MustCompile(`(?m)^\s*pid = (\d+)\s*$`)
var launchdPrintStateRE = regexp.MustCompile(`(?m)^\s*state = (\S+)\s*$`)
var launchdPrintJobStateRE = regexp.MustCompile(`(?m)^\s*job state = (.+?)\s*$`)
var launchdPrintLastExitRE = regexp.MustCompile(`(?m)^\s*last exit code = (.+?)\s*$`)
var launchdPrintPropertiesRE = regexp.MustCompile(`(?m)^\s*properties = (.+?)\s*$`)

// AgentLaunchdSpawnFailure describes why launchd cannot spawn the supervised
// agent, or returns "" when the loaded job shows no spawn failure. launchd
// records such failures only in its job state; the agent itself never runs,
// so its log stays silent.
func AgentLaunchdSpawnFailure() string {
	if !launchdSupportedFunc() {
		return ""
	}
	out, err := launchctlRunFunc("print", launchdGUITarget())
	if err != nil {
		return ""
	}
	jobState := ""
	if match := launchdPrintJobStateRE.FindSubmatch(out); match != nil {
		jobState = string(match[1])
	}
	needsConstraintUpdate := false
	if match := launchdPrintPropertiesRE.FindSubmatch(out); match != nil {
		for property := range strings.SplitSeq(string(match[1]), "|") {
			if strings.TrimSpace(property) == "needs LWCR update" {
				needsConstraintUpdate = true
			}
		}
	}
	if jobState != "spawn failed" && !needsConstraintUpdate {
		return ""
	}
	details := []string{}
	if jobState != "" {
		details = append(details, "job state "+jobState)
	}
	if match := launchdPrintLastExitRE.FindSubmatch(out); match != nil {
		details = append(details, "last exit code "+string(match[1]))
	}
	if needsConstraintUpdate {
		details = append(details, "launch constraints still name a previous executable")
	}
	return strings.Join(details, "; ")
}

// AgentLaunchdStatusForSocket reports supervision truth for the agent that
// owns socketPath. SupervisesSocket is false when the installed plist manages
// a different agent home (for example test homes), so callers never treat a
// foreign plist as their supervisor.
func AgentLaunchdStatusForSocket(socketPath string) LaunchdAgentStatus {
	status := LaunchdAgentStatus{
		Supported: launchdSupportedFunc(),
		Label:     AgentLaunchdLabel,
	}
	plistPath, err := AgentLaunchdPlistPath()
	if err != nil {
		return status
	}
	status.PlistPath = plistPath
	data, err := os.ReadFile(plistPath)
	if err != nil {
		return status
	}
	status.PlistPresent = true
	socketPath = strings.TrimSpace(socketPath)
	if socketPath != "" {
		status.SupervisesSocket = strings.Contains(string(data), "<string>"+plistEscape(socketPath)+"</string>")
	}
	if !status.Supported {
		return status
	}
	out, err := launchctlRunFunc("print", launchdGUITarget())
	if err != nil {
		return status
	}
	status.Loaded = true
	if match := launchdPrintPIDRE.FindSubmatch(out); match != nil {
		status.PID, _ = strconv.Atoi(string(match[1]))
	}
	if match := launchdPrintStateRE.FindSubmatch(out); match != nil {
		status.Running = string(match[1]) == "running"
	}
	if status.PID > 0 {
		status.Running = true
	}
	return status
}

func retryLaunchctl(window time.Duration, args ...string) error {
	deadline := time.Now().Add(window)
	for {
		out, err := launchctlRunFunc(args...)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("launchctl %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
		}
		launchdSleepFunc(250 * time.Millisecond)
	}
}

// startSupervisedAgentProcess routes agent starts through launchd when the
// installed supervised plist manages the requested socket. It returns true
// when launchd owns the start, so callers never spawn an unsupervised agent
// that races the KeepAlive respawn.
func startSupervisedAgentProcess(paths Paths) bool {
	if !launchdSupportedFunc() {
		return false
	}
	status := AgentLaunchdStatusForSocket(paths.SocketPath)
	if !status.PlistPresent || !status.SupervisesSocket {
		return false
	}
	if !status.Loaded {
		return BootstrapAgentLaunchd() == nil
	}
	if err := KickstartAgentLaunchd(false); err != nil {
		// A KeepAlive respawn can win the race with kickstart; a running
		// supervised agent is success regardless of who started it.
		return AgentLaunchdStatusForSocket(paths.SocketPath).Running
	}
	return true
}
