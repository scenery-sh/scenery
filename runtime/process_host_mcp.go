package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"scenery.sh/errs"
	"scenery.sh/internal/mcpcontract"
)

// In a process-model session the host runs assistant gateways, and their MCP
// tools are registered by service processes. The host dispatcher forwards each
// tool call to the owning process of the generation its assistant run executes
// (see process_host_conversations.go), pinning the call and its internal calls
// to that generation.

var activeProcessHost struct {
	sync.RWMutex
	host *processHost
}

func setActiveProcessHost(host *processHost) {
	activeProcessHost.Lock()
	activeProcessHost.host = host
	activeProcessHost.Unlock()
}

// assistantMCPDispatcher selects the dispatcher of an assistant MCP gateway.
func assistantMCPDispatcher() mcpcontract.ToolDispatcher {
	activeProcessHost.RLock()
	host := activeProcessHost.host
	activeProcessHost.RUnlock()
	if host == nil {
		return MCPToolDispatcher{}
	}
	return processHostMCPDispatcher{host: host}
}

type processHostMCPDispatcher struct {
	host *processHost
}

func (d processHostMCPDispatcher) CallTool(ctx context.Context, call mcpcontract.ToolCallContext, name string, input json.RawMessage) (mcpcontract.ToolOutcome, error) {
	process, err := d.host.mcpToolOwner(strings.TrimSpace(call.AssistantAddress), strings.TrimSpace(name))
	if err != nil {
		return mcpcontract.ToolOutcome{}, err
	}
	body, err := json.Marshal(processMCPCallRequest{Call: call, Name: name, Input: input})
	if err != nil {
		return mcpcontract.ToolOutcome{}, ContractSystemError(err)
	}
	generation, err := d.host.runGeneration(call)
	if err != nil {
		return mcpcontract.ToolOutcome{}, err
	}
	response, err := d.host.callProcessIn(ctx, generation, process, processMCPCallPath, body)
	if err != nil {
		return mcpcontract.ToolOutcome{}, err
	}
	if response.Outcome == nil {
		return mcpcontract.ToolOutcome{}, ContractSystemError(fmt.Errorf("service process %s returned no MCP outcome", process))
	}
	return *response.Outcome, nil
}

func (h *processHost) mcpToolOwner(assistantAddress, name string) (string, error) {
	if process := h.mcpTools[assistantAddress+"\x00"+name]; assistantAddress != "" && process != "" {
		return process, nil
	}
	match := ""
	for key, process := range h.mcpTools {
		if strings.HasSuffix(key, "\x00"+name) && (assistantAddress == "" || strings.HasPrefix(key, assistantAddress+"\x00")) {
			if match != "" {
				return "", mcpError(errs.InvalidArgument, "MCP tool %s is ambiguous", name)
			}
			match = process
		}
	}
	if match == "" {
		return "", mcpError(errs.NotFound, "MCP tool not found")
	}
	return match, nil
}

// callProcessIn sends one host-originated call to a service process of the
// generation number, or the current one when number is 0, pinned to that
// generation, and verifies the answering identity.
func (h *processHost) callProcessIn(ctx context.Context, number uint64, process, path string, body []byte) (processMCPResponse, error) {
	generation := h.acquire(number)
	if generation == nil {
		message := "application generation is not published"
		if number != 0 {
			message = fmt.Sprintf("application generation %d is not dispatchable", number)
		}
		return processMCPResponse{}, &errs.Error{Code: errs.Unavailable, Message: message, Meta: errs.Metadata{"delivery": "not_sent"}}
	}
	defer generation.inFlight.Add(-1)
	instance := generation.instances[process]
	if instance == nil {
		return processMCPResponse{}, &errs.Error{Code: errs.Unavailable, Message: fmt.Sprintf(processHostUnavailableReason, process), Meta: errs.Metadata{"delivery": "not_sent"}}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://scenery-process"+path, bytes.NewReader(body))
	if err != nil {
		return processMCPResponse{}, err
	}
	request.Header.Set("Authorization", "Bearer "+h.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(processGenerationHeader, strconv.FormatUint(generation.number, 10))
	response, err := instance.client.Do(request)
	if err != nil {
		return processMCPResponse{}, processLinkCallFailure(ctx, process, err)
	}
	defer func() { _ = response.Body.Close() }()
	if !processIdentityMatches(response.Header, instance.spec) {
		return processMCPResponse{}, &errs.Error{Code: errs.Unavailable, Message: (&processIdentityMismatchError{process: process}).Error(), Meta: errs.Metadata{"delivery": "unknown"}}
	}
	var decoded processMCPResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, processLinkMaxBody)).Decode(&decoded); err != nil {
		return processMCPResponse{}, processLinkCallFailure(ctx, process, err)
	}
	if decoded.Error != nil {
		return processMCPResponse{}, decoded.Error.err()
	}
	if response.StatusCode != http.StatusOK {
		return processMCPResponse{}, ContractSystemError(fmt.Errorf("service process %s answered HTTP %d", process, response.StatusCode))
	}
	return decoded, nil
}
