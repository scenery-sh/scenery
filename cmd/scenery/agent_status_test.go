package main

import (
	"context"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	localagent "scenery.sh/internal/agent"
)

type stubStatusSubstrateClient struct {
	substrates map[string]localagent.Substrate
	deleted    []string
}

func (c *stubStatusSubstrateClient) ListSubstrates(context.Context) ([]localagent.Substrate, error) {
	items := make([]localagent.Substrate, 0, len(c.substrates))
	for _, substrate := range c.substrates {
		items = append(items, substrate)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Kind < items[j].Kind })
	return items, nil
}

func (c *stubStatusSubstrateClient) DeleteSubstrate(_ context.Context, kind string) (localagent.Substrate, error) {
	kind = strings.TrimSpace(kind)
	substrate, ok := c.substrates[kind]
	if !ok {
		return localagent.Substrate{}, statusSubstrateNotFoundError(http.MethodDelete, kind)
	}
	delete(c.substrates, kind)
	c.deleted = append(c.deleted, kind)
	return substrate, nil
}

func (c *stubStatusSubstrateClient) GetSubstrate(_ context.Context, kind string) (localagent.Substrate, error) {
	kind = strings.TrimSpace(kind)
	if substrate, ok := c.substrates[kind]; ok {
		return substrate, nil
	}
	return localagent.Substrate{}, statusSubstrateNotFoundError(http.MethodGet, kind)
}

func statusSubstrateNotFoundError(method, kind string) error {
	return &localagent.HTTPError{
		Method:     method,
		Path:       "/v1/substrates/" + kind,
		Status:     http.StatusText(http.StatusNotFound),
		StatusCode: http.StatusNotFound,
	}
}

func TestStatusSubstratesPrunesDeadOwners(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	livePID := os.Getpid()
	liveExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	liveOwner := localagent.Owner{PID: livePID, Exe: liveExecutable}
	deadOwner := localagent.Owner{PID: 99999991, Exe: "/missing/dead-substrate-owner"}
	client := &stubStatusSubstrateClient{substrates: map[string]localagent.Substrate{
		"live": {Kind: "live", OwnerPID: liveOwner.PID, Owner: liveOwner},
		"dead": {Kind: "dead", OwnerPID: deadOwner.PID, Owner: deadOwner},
	}}

	substrates, err := statusSubstrates(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if len(substrates) != 1 || substrates[0].Kind != "live" {
		t.Fatalf("substrates = %+v, want only live", substrates)
	}
	if len(client.deleted) != 1 || client.deleted[0] != "dead" {
		t.Fatalf("deleted substrates = %v, want [dead]", client.deleted)
	}
	if _, err := client.GetSubstrate(ctx, "dead"); !localagent.IsNotFound(err) {
		t.Fatalf("dead substrate still registered: %v", err)
	}
}
