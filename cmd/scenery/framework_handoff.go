package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"scenery.sh/internal/build"
	"scenery.sh/internal/envpolicy"
)

// frameworkHandoff ends this producer's `scenery up` so that the producer the
// application now selects continues it. It travels as an error value out of
// runWithWatch, so the ordinary deferred shutdown stops the runtime first.
type frameworkHandoff struct {
	Executable   string
	Version      string
	SourceDigest string
}

func (h *frameworkHandoff) Error() string {
	return fmt.Sprintf("scenery up continues with Scenery %s", h.label())
}

func (h *frameworkHandoff) label() string {
	return frameworkLabel(h.Version, h.SourceDigest)
}

func (h *frameworkHandoff) summary() map[string]any {
	return map[string]any{"executable": h.Executable, "framework_version": h.Version, "source_digest": h.SourceDigest}
}

func frameworkLabel(version, sourceDigest string) string {
	if version != "" && version != "dev" {
		return version
	}
	if short := strings.TrimPrefix(sourceDigest, "sha256:"); len(short) >= 12 {
		return "source " + short[:12]
	}
	return "source"
}

func desiredFrameworkLabel(desired build.DesiredFramework, root string) string {
	if desired.Root == "" {
		return desired.Version
	}
	digest, _ := build.FrameworkSnapshotDigest(root, desired.Root)
	return frameworkLabel("dev", digest)
}

var execFrameworkHandoff = func(executable string, args, environment []string) error {
	return execProcess(executable, append([]string{executable}, args...), environment)
}

var (
	changedAppFrameworkFunc     = changedAppFramework
	prepareFrameworkHandoffFunc = prepareFrameworkHandoff
)

// changedAppFramework reports the framework the application now selects when
// this process is a producer `scenery framework use` prepared for root and the
// selection names another one.
func changedAppFramework(root string) (build.DesiredFramework, bool) {
	if !processReplacementSupported || !build.RunsPreparedFramework(root) {
		return build.DesiredFramework{}, false
	}
	desired, err := build.ReadDesiredFramework(root)
	if err != nil {
		// The build's framework verification reports the authored error.
		return build.DesiredFramework{}, false
	}
	return desired, frameworkSelectionDiffers(root, desired, sceneryVersion, build.LinkedFrameworkDigest())
}

// frameworkSelectionDiffers compares the authored selection with a producer
// without reading framework source: a pinned producer carries its module
// version, and an app-local snapshot directory is named by its digest.
func frameworkSelectionDiffers(root string, desired build.DesiredFramework, producerVersion, producerDigest string) bool {
	if desired.Root == "" {
		return desired.Version != producerVersion
	}
	// Another local replacement needs an explicit `scenery framework use`,
	// which rewrites go.mod; a running runtime never edits authored files.
	digest, ok := build.FrameworkSnapshotDigest(root, desired.Root)
	return ok && digest != producerDigest
}

// prepareFrameworkHandoff prepares the selected framework exactly as
// `scenery framework use` does, and lets that producer publish its own
// receipt. It returns nil when the selection resolves to this executable.
func prepareFrameworkHandoff(ctx context.Context, root string) (*frameworkHandoff, error) {
	selection, _, err := useAppFramework(ctx, root, "", true)
	if err != nil {
		return nil, err
	}
	if build.OwnsFrameworkSelection(selection) {
		return nil, nil
	}
	// The new producer finalizes its receipt in its own protocol; only its
	// exit status matters here, never its output decoded by this producer.
	command := exec.CommandContext(ctx, selection.Executable, "framework", "use", "--app-root", root, "-o", "json")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		const limit = 2048
		output = bytes.TrimSpace(output)
		if len(output) > limit {
			output = output[:limit]
		}
		return nil, fmt.Errorf("publish the Scenery %s framework selection: %w: %s", frameworkLabel(selection.Version, selection.Source.Digest), err, output)
	}
	return &frameworkHandoff{Executable: selection.Executable, Version: selection.Version, SourceDigest: selection.Source.Digest}, nil
}

// startupFrameworkHandoff runs before a foreground or launcher `scenery up`
// acquires anything: when the app selects another framework, that producer
// starts the runtime instead. A failed preparation keeps this producer, whose
// build verification then reports the mismatch.
func startupFrameworkHandoff(appRootOption string) *frameworkHandoff {
	root, err := discoverFrameworkRoot(appRootOption)
	if err != nil {
		return nil
	}
	desired, changed := changedAppFramework(root)
	if !changed {
		return nil
	}
	label := desiredFrameworkLabel(desired, root)
	fmt.Fprintf(os.Stderr, "scenery: the app selects Scenery %s; preparing it before startup\n", label)
	// Nothing is owned yet: an interrupt may simply end the process.
	handoff, err := prepareFrameworkHandoff(context.Background(), root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scenery: Scenery %s could not be prepared; starting with this Scenery: %v\n", label, err)
		return nil
	}
	return handoff
}

// frameworkHandoffBeforeBuild prepares the framework the app now selects while
// the current runtime keeps serving. A preparation failure is reported once
// while that selection stays; the build's framework verification keeps
// reporting the mismatch. Selecting this producer again ends the episode, so a
// later selection of the failed framework is prepared anew.
func (s *devSupervisor) frameworkHandoffBeforeBuild(ctx context.Context, failed *build.DesiredFramework) *frameworkHandoff {
	desired, changed := changedAppFrameworkFunc(s.root)
	if !changed {
		*failed = build.DesiredFramework{}
		return nil
	}
	if desired == *failed {
		return nil
	}
	label := desiredFrameworkLabel(desired, s.root)
	var handoff *frameworkHandoff
	err := s.console.Phase("Preparing Scenery "+label+" for handoff", func() error {
		var err error
		handoff, err = prepareFrameworkHandoffFunc(ctx, s.root)
		return err
	})
	if err != nil {
		if ctx.Err() != nil {
			// Interrupted: the watch loop stops next; nothing failed.
			return nil
		}
		*failed = desired
		s.console.RebuildFailed(fmt.Errorf("the app now selects Scenery %s, which could not be prepared; the current runtime keeps serving: %w", label, err))
		return nil
	}
	if handoff != nil {
		s.console.FrameworkHandoff(handoff)
	}
	return handoff
}

// continueWithFramework replaces this process with the new producer after the
// runtime stopped. A foreground run keeps its arguments, terminal and PID. A
// detached supervisor relaunches through the new producer's own launcher, which
// alone can verify that producer's readiness and records.
func continueWithFramework(handoff *frameworkHandoff, upArgs []string) error {
	args := append([]string{"up"}, upArgs...)
	environment := envpolicy.Environ()
	if detachedDevChildMode() {
		args = append([]string{"up"}, detachedRelaunchArgs(upArgs)...)
		environment = environmentWithout(environment, detachedDevChildEnv)
	}
	if err := execFrameworkHandoff(handoff.Executable, args, environment); err != nil {
		return fmt.Errorf("the runtime stopped for its Scenery %s handoff, but %s could not start: %w; run scenery up again", handoff.label(), handoff.Executable, err)
	}
	return nil
}

// detachedRelaunchArgs turns a detached supervisor's arguments back into a
// detached launch. The launcher's JSON result lands in the retiring log.
func detachedRelaunchArgs(childArgs []string) []string {
	args := make([]string, 0, len(childArgs)+3)
	for index := 0; index < len(childArgs); index++ {
		argument := childArgs[index]
		if argument == "-o" {
			index++
			continue
		}
		if strings.HasPrefix(argument, "-o=") {
			continue
		}
		args = append(args, argument)
	}
	return append(args, "--detach", "-o", "json")
}

func environmentWithout(environment []string, name string) []string {
	kept := make([]string, 0, len(environment))
	for _, entry := range environment {
		if !strings.HasPrefix(entry, name+"=") {
			kept = append(kept, entry)
		}
	}
	return kept
}
