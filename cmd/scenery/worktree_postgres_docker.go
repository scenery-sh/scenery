package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/envpolicy"
)

type worktreeDockerClient struct {
	runner   postgresDockerRunner
	endpoint string
}

func newWorktreeDockerClient(ctx context.Context) (*worktreeDockerClient, error) {
	runner := execPostgresDockerRunner{}
	endpoint := strings.TrimSpace(envpolicy.Get("DOCKER_HOST"))
	if endpoint == "" || strings.TrimSpace(envpolicy.Get("DOCKER_CONTEXT")) != "" {
		out, err := runner.Run(ctx, "context", "inspect", "--format", "{{json .Endpoints.docker.Host}}")
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(out), &endpoint); err != nil {
			return nil, worktreePostgresPrecondition("the selected Docker context endpoint could not be read")
		}
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "unix" || u.Host != "" || !strings.HasPrefix(u.Path, "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, worktreePostgresPrecondition("local managed PostgreSQL requires a local Unix Docker endpoint with host-loopback access")
	}
	// Freeze endpoint selection for this operation without publishing temporary
	// environment changes to other capabilities or goroutines.
	var environment []string
	for _, entry := range envpolicy.Environ() {
		if !strings.HasPrefix(entry, "DOCKER_HOST=") && !strings.HasPrefix(entry, "DOCKER_CONTEXT=") {
			environment = append(environment, entry)
		}
	}
	return &worktreeDockerClient{runner: execPostgresDockerRunner{environment: environment}, endpoint: endpoint}, nil
}

func (d *worktreeDockerClient) run(ctx context.Context, args ...string) (string, error) {
	return d.runner.Run(ctx, append([]string{"--host", d.endpoint}, args...)...)
}

func (d *worktreeDockerClient) Identity(ctx context.Context) (string, string, error) {
	out, err := d.run(ctx, "info", "--format", "{{json .ID}}")
	if err != nil {
		return "", "", err
	}
	var id string
	if err := json.Unmarshal([]byte(out), &id); err != nil || id == "" {
		return "", "", worktreePostgresPrecondition("Docker did not return a stable daemon identity")
	}
	return id, d.endpoint, nil
}

// A missing local record is not permission to replace an orphaned database.
// Search by canonical root alone so changed app/user metadata cannot hide it.
func (d *worktreeDockerClient) HasRetainedResources(ctx context.Context, root string) (bool, error) {
	filter := "label=scenery.worktree.root=" + root
	for _, args := range [][]string{
		{"container", "ls", "--all", "--quiet", "--filter", filter},
		{"volume", "ls", "--quiet", "--filter", filter},
	} {
		out, err := d.run(ctx, args...)
		if err != nil {
			return false, err
		}
		if strings.TrimSpace(out) != "" {
			return true, nil
		}
	}
	return false, nil
}

func (d *worktreeDockerClient) mutate(ctx context.Context, record localagent.WorktreeRecord, args ...string) error {
	id, endpoint, err := d.Identity(ctx)
	if err != nil {
		return err
	}
	if id != record.Postgres.DaemonID || endpoint != record.Postgres.DaemonEndpoint {
		return worktreePostgresPrecondition("Docker daemon identity changed before mutation")
	}
	_, err = d.run(ctx, args...)
	return err
}

func (d *worktreeDockerClient) Inspect(ctx context.Context, record localagent.WorktreeRecord) (*worktreePostgresVolume, *worktreePostgresContainer, error) {
	p := record.Postgres
	volumeOutput, err := d.run(ctx, "volume", "inspect", p.Volume)
	var volume *worktreePostgresVolume
	if err != nil {
		if !isMissingDockerObject(volumeOutput, err) {
			return nil, nil, err
		}
	} else {
		var volumes []worktreePostgresVolume
		if err := json.Unmarshal([]byte(volumeOutput), &volumes); err != nil || len(volumes) != 1 {
			return nil, nil, worktreePostgresPrecondition("Docker volume inspection was malformed")
		}
		volume = &volumes[0]
	}
	containerOutput, err := d.run(ctx, "container", "inspect", p.Container)
	if err != nil {
		if isMissingDockerObject(containerOutput, err) {
			return volume, nil, nil
		}
		return nil, nil, err
	}
	container, err := decodeWorktreePostgresContainer(containerOutput)
	return volume, container, err
}

type worktreeDockerPort struct {
	HostIP   string
	HostPort string
}

func decodeWorktreePostgresContainer(encoded string) (*worktreePostgresContainer, error) {
	var containers []struct {
		ID, Name string
		Config   struct {
			Image  string
			Labels map[string]string
		}
		State  struct{ Running bool }
		Mounts []struct {
			Type, Name, Source, Destination string
			RW                              bool
		}
		HostConfig struct {
			PortBindings map[string][]worktreeDockerPort
		}
		NetworkSettings struct {
			Ports map[string][]worktreeDockerPort
		}
	}
	if err := json.Unmarshal([]byte(encoded), &containers); err != nil || len(containers) != 1 {
		return nil, worktreePostgresPrecondition("Docker container inspection was malformed")
	}
	c := containers[0]
	bindings := c.HostConfig.PortBindings["5432/tcp"]
	if len(bindings) != 1 || len(c.HostConfig.PortBindings) != 1 || bindings[0].HostIP != "127.0.0.1" || len(c.Mounts) != 1 || c.Mounts[0].Type != "volume" {
		return nil, worktreePostgresPrecondition("the container has unexpected mounts or network publication")
	}
	port := 0
	if c.State.Running {
		actual := c.NetworkSettings.Ports["5432/tcp"]
		if len(actual) != 1 || actual[0].HostIP != "127.0.0.1" {
			return nil, worktreePostgresPrecondition("the running database is not published only on host loopback")
		}
		var err error
		port, err = strconv.Atoi(actual[0].HostPort)
		if err != nil || port < 1 || port > 65535 {
			return nil, worktreePostgresPrecondition("the running database has no valid observed host port")
		}
	}
	return &worktreePostgresContainer{
		ID: c.ID, Name: strings.TrimPrefix(c.Name, "/"), Image: c.Config.Image,
		Volume: c.Mounts[0].Name, Mountpoint: c.Mounts[0].Destination, MountSource: c.Mounts[0].Source,
		Running: c.State.Running, Writable: c.Mounts[0].RW, Port: port, HostIP: bindings[0].HostIP, Labels: c.Config.Labels,
	}, nil
}

func worktreeDockerLabelArgs(record localagent.WorktreeRecord) []string {
	labels := worktreePostgresLabels(record)
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var args []string
	for _, key := range keys {
		args = append(args, "--label", key+"="+labels[key])
	}
	return args
}

func (d *worktreeDockerClient) CreateVolume(ctx context.Context, record localagent.WorktreeRecord) error {
	args := append([]string{"volume", "create", "--driver", "local"}, worktreeDockerLabelArgs(record)...)
	return d.mutate(ctx, record, append(args, record.Postgres.Volume)...)
}

func (d *worktreeDockerClient) CreateContainer(ctx context.Context, record localagent.WorktreeRecord) error {
	p := record.Postgres
	args := append([]string{"container", "create", "--name", p.Container}, worktreeDockerLabelArgs(record)...)
	args = append(args, "--publish", "127.0.0.1::5432", "--mount", "type=volume,src="+p.Volume+",dst=/var/lib/postgresql",
		"--env", "POSTGRES_USER="+p.User, "--env", "POSTGRES_PASSWORD="+p.Password, p.Image)
	return d.mutate(ctx, record, args...)
}

func (d *worktreeDockerClient) Start(ctx context.Context, record localagent.WorktreeRecord, id string) error {
	return d.mutate(ctx, record, "container", "start", id)
}

func (d *worktreeDockerClient) Stop(ctx context.Context, record localagent.WorktreeRecord, id string) error {
	return d.mutate(ctx, record, "container", "stop", "--time", "10", id)
}

func (d *worktreeDockerClient) RemoveContainer(ctx context.Context, record localagent.WorktreeRecord, id string) error {
	return d.mutate(ctx, record, "container", "rm", id)
}

func (d *worktreeDockerClient) RemoveVolume(ctx context.Context, record localagent.WorktreeRecord) error {
	return d.mutate(ctx, record, "volume", "rm", record.Postgres.Volume)
}

func worktreePostgresURL(p *localagent.WorktreePostgres, database string) string {
	u := &url.URL{Scheme: "postgres", User: url.UserPassword(p.User, p.Password), Host: fmt.Sprintf("127.0.0.1:%d", p.Port), Path: "/" + database}
	return u.String()
}

func probeWorktreePostgres(ctx context.Context, p *localagent.WorktreePostgres) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		db, err := openPostgresDatabase(ctx, worktreePostgresURL(p, "postgres"))
		if err == nil {
			var systemID string
			err = db.QueryRowContext(ctx, "SELECT system_identifier::text FROM pg_control_system()").Scan(&systemID)
			_ = db.Close()
			if err == nil {
				return systemID, nil
			}
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
}
