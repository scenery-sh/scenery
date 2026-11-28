package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
)

const pruneOrphanedKind = "scenery.prune.orphaned"

type pruneOrphanedDatabase struct {
	Container string `json:"container"`
	Volume    string `json:"volume"`
}

// pruneOrphanedRecord is the retained state of one checkout that no longer
// exists. Bytes are logical file bytes of its state directory; the database
// volume is unmeasured.
type pruneOrphanedRecord struct {
	Key       string                 `json:"key"`
	AppRoot   string                 `json:"app_root"`
	Path      string                 `json:"path"`
	UpdatedAt string                 `json:"updated_at"`
	Bytes     int64                  `json:"bytes"`
	Complete  bool                   `json:"complete"`
	Database  *pruneOrphanedDatabase `json:"database"`
	Selected  bool                   `json:"selected"`
	Removed   bool                   `json:"removed"`
	Reason    string                 `json:"reason,omitempty"`
}

type pruneOrphanedResponse struct {
	cliPayloadIdentity
	Cutoff        string                `json:"cutoff"`
	Applied       bool                  `json:"applied"`
	Revision      string                `json:"revision"`
	Records       []pruneOrphanedRecord `json:"records"`
	SelectedBytes int64                 `json:"selected_bytes"`
	Removed       int                   `json:"removed"`
	Failed        int                   `json:"failed"`
	Next          string                `json:"next,omitempty"`
}

// newOrphanedWorktreeDocker is replaced by tests, which never reach Docker.
var newOrphanedWorktreeDocker = func(ctx context.Context) (worktreePostgresDocker, error) {
	return newWorktreeDockerClient(ctx)
}

type orphanedDockerLister interface {
	ListManagedContainers(context.Context) ([]worktreePostgresContainer, error)
}

// orphanedWorktreeSelection is one orphan with the verdict a preview and its
// application must agree on.
type orphanedWorktreeSelection struct {
	orphan localagent.OrphanedWorktree
	reason string
}

// runPruneOrphaned previews or removes the retained state of checkouts that
// no longer exist: the state directory and the managed database container and
// volume its record names. Nothing else can reclaim them, because retained
// metadata is upgraded from its checkout and removal needs compatible
// metadata. Removal deletes application data, so it applies only to the exact
// selection a preview reported.
func runPruneOrphaned(ctx context.Context, stdout io.Writer, opts pruneOptions) error {
	home, err := commandAgentPaths()
	if err != nil {
		return err
	}
	cutoff := time.Now().UTC().Add(-opts.OlderThan)
	orphans, err := localagent.DiscoverOrphanedWorktrees(home.Home)
	if err != nil {
		return err
	}
	docker, dockerErr := newOrphanedWorktreeDocker(ctx)
	orphans = append(orphans, discoverUnrecordedDockerOrphans(ctx, docker, dockerErr, orphans)...)
	selection := make([]orphanedWorktreeSelection, 0, len(orphans))
	for _, orphan := range orphans {
		item := orphanedWorktreeSelection{orphan: orphan}
		switch {
		case orphan.UpdatedAt.After(cutoff):
			item.reason = "retained state was written after the cutoff"
		case orphan.Postgres != nil:
			_, _, item.reason = inspectOrphanedDatabase(ctx, docker, dockerErr, orphan)
		}
		selection = append(selection, item)
	}
	response := pruneOrphanedResponse{cliPayloadIdentity: newCLIPayloadIdentity(pruneOrphanedKind), Cutoff: cutoff.Format(time.RFC3339Nano), Applied: opts.Yes, Revision: orphanedSelectionRevision(selection), Records: []pruneOrphanedRecord{}}
	if opts.Yes && opts.ExpectRevision != response.Revision {
		return preconditionErrorf("the orphaned selection is %s, not %s; preview again with scenery prune --orphaned --older-than <duration> and apply the revision it reports", response.Revision, opts.ExpectRevision)
	}
	for _, item := range selection {
		orphan := item.orphan
		record := pruneOrphanedRecord{Key: orphan.Key, AppRoot: orphan.AppRoot, Path: orphan.Directory, UpdatedAt: orphan.UpdatedAt.Format(time.RFC3339Nano), Selected: item.reason == "", Reason: item.reason}
		record.Bytes, record.Complete = worktreeInventoryBytes(orphan.Directory)
		if orphan.Postgres != nil {
			record.Database = &pruneOrphanedDatabase{Container: orphan.Postgres.Container, Volume: orphan.Postgres.Volume}
		}
		if record.Selected {
			response.SelectedBytes += record.Bytes
		}
		if record.Selected && opts.Yes {
			if err := removeOrphanedWorktree(ctx, home.Home, docker, dockerErr, orphan); err != nil {
				record.Reason = err.Error()
				response.Failed++
			} else {
				record.Removed = true
				response.Removed++
			}
		}
		response.Records = append(response.Records, record)
	}
	if !opts.Yes && orphanedSelected(response.Records) > 0 {
		response.Next = fmt.Sprintf("scenery prune --orphaned --older-than %s --yes --expect-revision %s", opts.OlderThan, response.Revision)
	}
	if opts.JSON {
		if err := writeCLIJSON(stdout, response); err != nil {
			return err
		}
	} else {
		writePruneOrphanedText(stdout, response)
	}
	if response.Failed > 0 {
		return preconditionErrorf("%d orphaned worktree record(s) were not removed; each keeps its state and reports why", response.Failed)
	}
	return nil
}

func orphanedSelected(records []pruneOrphanedRecord) int {
	count := 0
	for _, record := range records {
		if record.Selected {
			count++
		}
	}
	return count
}

func writePruneOrphanedText(stdout io.Writer, response pruneOrphanedResponse) {
	for _, record := range response.Records {
		state := "kept"
		switch {
		case record.Removed:
			state = "removed"
		case record.Selected && !response.Applied:
			state = "selected"
		}
		database := ""
		if record.Database != nil {
			database = " + database volume " + record.Database.Volume
		}
		line := fmt.Sprintf("%s\t%s\t%d logical bytes%s", state, record.AppRoot, record.Bytes, database)
		if record.Reason != "" {
			line += ": " + record.Reason
		}
		_, _ = fmt.Fprintln(stdout, line)
	}
	if response.Applied {
		_, _ = fmt.Fprintf(stdout, "removed %d orphaned worktree record(s); %d failed\n", response.Removed, response.Failed)
		return
	}
	_, _ = fmt.Fprintf(stdout, "%d of %d orphaned worktree record(s) selected, %d logical bytes; nothing was removed\n", orphanedSelected(response.Records), len(response.Records), response.SelectedBytes)
	if response.Next != "" {
		_, _ = fmt.Fprintf(stdout, "apply: %s\n", response.Next)
	}
}

// orphanedSelectionRevision names the exact records a removal would delete:
// each selected key with the digest of its retained record.
func orphanedSelectionRevision(selection []orphanedWorktreeSelection) string {
	var lines []string
	for _, item := range selection {
		if item.reason == "" {
			lines = append(lines, item.orphan.Key+" "+item.orphan.RecordDigest)
		}
	}
	slices.Sort(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// inspectOrphanedDatabase verifies the database resources an orphaned record
// claims and returns why they may not be removed, or "". A record that cannot
// be decoded strictly still names its resources, so every binding the current
// producer would check is checked against Docker instead: the daemon, the
// managed names and the ownership labels of root, application, user and
// instance.
func inspectOrphanedDatabase(ctx context.Context, docker worktreePostgresDocker, dockerErr error, orphan localagent.OrphanedWorktree) (localagent.WorktreeRecord, *worktreePostgresContainer, string) {
	p := orphan.Postgres
	record := localagent.WorktreeRecord{AppRoot: orphan.AppRoot, AppID: orphan.AppID, UserID: orphan.UserID, Postgres: &localagent.WorktreePostgres{
		InstanceID: p.InstanceID, DaemonID: p.DaemonID, DaemonEndpoint: p.DaemonEndpoint, Container: p.Container, Volume: p.Volume,
	}}
	if len(p.InstanceID) != 32 || strings.Trim(p.InstanceID, "0123456789abcdef") != "" || p.Container != "scenery-pg-"+p.InstanceID || p.Volume != "scenery-pg-data-"+p.InstanceID {
		return record, nil, "its record names database resources outside the managed naming"
	}
	if dockerErr != nil || docker == nil {
		return record, nil, "its database cannot be verified because Docker is unavailable"
	}
	id, endpoint, err := docker.Identity(ctx)
	if err != nil {
		return record, nil, "its database cannot be verified because Docker is unavailable"
	}
	if id != p.DaemonID || endpoint != p.DaemonEndpoint {
		return record, nil, "its database belongs to another Docker daemon (" + p.DaemonEndpoint + ")"
	}
	volume, container, err := docker.Inspect(ctx, record)
	if err != nil {
		return record, nil, "its database resources could not be inspected: " + err.Error()
	}
	if volume != nil && (volume.Name != p.Volume || verifyWorktreePostgresLabels(record, volume.Labels) != nil) {
		return record, nil, "its database volume does not carry this worktree's ownership labels"
	}
	if container != nil && (container.Name != p.Container || container.Volume != p.Volume || verifyWorktreePostgresLabels(record, container.Labels) != nil) {
		return record, nil, "its database container does not carry this worktree's ownership labels"
	}
	return record, container, ""
}

func discoverUnrecordedDockerOrphans(ctx context.Context, docker worktreePostgresDocker, dockerErr error, existing []localagent.OrphanedWorktree) []localagent.OrphanedWorktree {
	if dockerErr != nil || docker == nil {
		return nil
	}
	lister, ok := docker.(orphanedDockerLister)
	if !ok {
		return nil
	}
	daemonID, daemonEndpoint, err := docker.Identity(ctx)
	if err != nil {
		return nil
	}
	containers, err := lister.ListManagedContainers(ctx)
	if err != nil {
		return nil
	}
	knownInstances := map[string]bool{}
	for _, orphan := range existing {
		if orphan.Postgres != nil && orphan.Postgres.InstanceID != "" {
			knownInstances[orphan.Postgres.InstanceID] = true
		}
	}
	currentUID := os.Getuid()
	currentUIDStr := strconv.Itoa(currentUID)
	var discovered []localagent.OrphanedWorktree
	for _, c := range containers {
		instanceID := c.Labels["scenery.resource.instance"]
		appRoot := c.Labels["scenery.worktree.root"]
		appID := c.Labels["scenery.worktree.app"]
		if instanceID == "" || appRoot == "" || appID == "" || knownInstances[instanceID] {
			continue
		}
		if c.Labels["scenery.worktree.user"] != currentUIDStr {
			continue
		}
		if len(instanceID) != 32 || strings.Trim(instanceID, "0123456789abcdef") != "" {
			continue
		}
		if c.Name != "scenery-pg-"+instanceID || c.Volume != "scenery-pg-data-"+instanceID {
			continue
		}
		if _, statErr := os.Stat(appRoot); !os.IsNotExist(statErr) {
			continue
		}
		knownInstances[instanceID] = true
		sum := sha256.Sum256([]byte(daemonID + "\x00" + instanceID + "\x00" + appRoot))
		discovered = append(discovered, localagent.OrphanedWorktree{
			Key:          "docker:" + instanceID,
			AppRoot:      appRoot,
			AppID:        appID,
			UserID:       currentUID,
			RecordDigest: "sha256:" + hex.EncodeToString(sum[:]),
			Postgres: &localagent.OrphanedWorktreePostgres{
				InstanceID:     instanceID,
				DaemonID:       daemonID,
				DaemonEndpoint: daemonEndpoint,
				Container:      c.Name,
				Volume:         c.Volume,
			},
		})
	}
	return discovered
}

func removeOrphanedWorktree(ctx context.Context, home string, docker worktreePostgresDocker, dockerErr error, orphan localagent.OrphanedWorktree) error {
	if orphan.Directory == "" {
		if orphan.Postgres == nil {
			return nil
		}
		return removeOrphanedDatabase(ctx, docker, dockerErr, orphan)
	}
	paths, err := localagent.PathsForWorktree(home, orphan.AppRoot)
	if err != nil {
		return err
	}
	return paths.RemoveOrphaned(orphan.RecordDigest, func(current localagent.OrphanedWorktree) error {
		if current.Postgres == nil {
			return nil
		}
		return removeOrphanedDatabase(ctx, docker, dockerErr, current)
	})
}

// removeOrphanedDatabase removes the verified container and volume and
// confirms both are absent, so the state directory holding their credentials
// outlives a failed deletion.
func removeOrphanedDatabase(ctx context.Context, docker worktreePostgresDocker, dockerErr error, orphan localagent.OrphanedWorktree) error {
	record, container, reason := inspectOrphanedDatabase(ctx, docker, dockerErr, orphan)
	if reason != "" {
		return fmt.Errorf("%s", reason)
	}
	if container != nil {
		if container.Running {
			if err := docker.Stop(ctx, record, container.ID); err != nil {
				return err
			}
		}
		if err := docker.RemoveContainer(ctx, record, container.ID); err != nil {
			return err
		}
	}
	volume, container, err := docker.Inspect(ctx, record)
	if err != nil {
		return err
	}
	if container != nil {
		return fmt.Errorf("database container deletion was not confirmed")
	}
	if volume != nil {
		if err := docker.RemoveVolume(ctx, record); err != nil {
			return err
		}
	}
	volume, container, err = docker.Inspect(ctx, record)
	if err != nil {
		return err
	}
	if volume != nil || container != nil {
		return fmt.Errorf("database resource deletion was not confirmed")
	}
	return nil
}
