package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"scenery.sh/errs"
	"scenery.sh/internal/mcpcontract"
	"scenery.sh/internal/mcpgateway"
)

// In a process-model session the host runs assistant gateways, and their MCP
// tools are registered by service processes. The host dispatcher forwards each
// tool call to the owning process of the current generation, pinning the call
// and its internal calls to that generation. The host authorizes durable
// receipts: it records the principal, owning process, durable service and task
// of each accepted receipt in the session's receipt journal (see
// process_host_receipts.go), and sends authorized status and cancellation to
// the owning process of the current generation, which reads the shared durable
// store.

var activeProcessHost struct {
	sync.RWMutex
	host *processHost
}

func setActiveProcessHost(host *processHost) {
	activeProcessHost.Lock()
	activeProcessHost.host = host
	activeProcessHost.Unlock()
}

// assistantMCPDispatchers selects the dispatcher of an assistant MCP gateway.
func assistantMCPDispatchers() (mcpcontract.ToolDispatcher, mcpgateway.DurableOperations) {
	activeProcessHost.RLock()
	host := activeProcessHost.host
	activeProcessHost.RUnlock()
	if host == nil {
		return MCPToolDispatcher{}, MCPToolDispatcher{}
	}
	dispatcher := processHostMCPDispatcher{host: host}
	return dispatcher, dispatcher
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
	generation, err := d.host.conversationGeneration(processHostConversationKey(call.AssistantAddress, call.Principal, call.ConversationDigest))
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
	if receipt := response.Outcome.Receipt; receipt != nil && receipt.ExecutionID != "" && response.Durable != nil {
		d.host.owners.store(strings.TrimSpace(call.Principal), receipt.ExecutionID, processHostDurableOwner{
			process: process, service: strings.TrimSpace(response.Durable.Service), taskName: strings.TrimSpace(response.Durable.TaskName),
		})
	}
	return *response.Outcome, nil
}

func (d processHostMCPDispatcher) Status(ctx context.Context, call mcpcontract.ToolCallContext, executionID string) (json.RawMessage, error) {
	return d.durable(ctx, "status", call, executionID)
}

func (d processHostMCPDispatcher) Cancel(ctx context.Context, call mcpcontract.ToolCallContext, executionID string) (json.RawMessage, error) {
	return d.durable(ctx, "cancel", call, executionID)
}

func (d processHostMCPDispatcher) durable(ctx context.Context, operation string, call mcpcontract.ToolCallContext, executionID string) (json.RawMessage, error) {
	executionID = strings.TrimSpace(executionID)
	owner, ok := d.host.owners.load(strings.TrimSpace(call.Principal), executionID)
	if !ok || owner.service == "" || owner.taskName == "" {
		return nil, errors.New("not_found: durable execution not found")
	}
	body, err := json.Marshal(processMCPDurableRequest{Operation: operation, Call: call, ExecutionID: executionID, Service: owner.service, TaskName: owner.taskName})
	if err != nil {
		return nil, ContractSystemError(err)
	}
	response, err := d.host.callProcess(ctx, owner.process, processMCPDurablePath, body)
	if err != nil {
		return nil, err
	}
	return response.Result, nil
}

func (h *processHost) mcpToolOwner(assistantAddress, name string) (string, error) {
	if process := h.mcpTools[assistantAddress+"\x00"+name]; assistantAddress != "" && process != "" {
		return process, nil
	}
	match := ""
	for key, process := range h.mcpTools {
		if strings.HasSuffix(key, "\x00"+name) && (assistantAddress == "" || strings.HasPrefix(key, assistantAddress+"\x00")) {
			if match != "" {
				return "", fmt.Errorf("invalid_argument: MCP tool %s is ambiguous", name)
			}
			match = process
		}
	}
	if match == "" {
		return "", errors.New("not_found: MCP tool not found")
	}
	return match, nil
}

// callProcess sends one host-originated call to a service process of the
// current generation, pinned to that generation, and verifies the answering
// identity.
func (h *processHost) callProcess(ctx context.Context, process, path string, body []byte) (processMCPResponse, error) {
	return h.callProcessIn(ctx, 0, process, path, body)
}

// callProcessIn is callProcess in the generation number, or the current one
// when number is 0.
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
