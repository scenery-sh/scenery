package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"scenery.sh/errs"
)

// Live CLI invocation: the supervisor sends a contract CLI request to the
// process host, which forwards it to the service process that owns the binding
// in the current generation. The service process answers the same
// scenery.contract-cli.response a request file produces.
const (
	processCLICallPath = "/__scenery/process/v1/cli/call"
	processHostCLIPath = "/__scenery/process/v1/cli"
)

// ProcessHostCLIRequest pins a live invocation to the generation verified by wait.
type ProcessHostCLIRequest struct {
	Request    ContractCLIRequest `json:"request"`
	Generation uint64             `json:"generation"`
}

// ProcessHostCLIResult is the host's answer to a live CLI request: the
// response and the generation and build identity that served it.
type ProcessHostCLIResult struct {
	Response         ContractCLIResponse `json:"response"`
	Generation       uint64              `json:"generation"`
	Process          string              `json:"process"`
	ContractRevision string              `json:"contract_revision"`
	// ImplementationRevision and BuildInputDigest identify the build of the
	// serving generation.
	ImplementationRevision string `json:"implementation_revision"`
	BuildInputDigest       string `json:"build_input_digest"`
}

func (s *server) handleProcessCLICall(w http.ResponseWriter, req *http.Request, _ routeParams) {
	if !authorizeProcessLinkRequest(w, req) {
		return
	}
	var request ContractCLIRequest
	if err := decodeProcessLinkBody(req, &request); err != nil || validateContractCLIRequest(request) != nil {
		writeProcessMCPResponse(w, http.StatusBadRequest, processMCPResponse{Error: &processLinkError{Kind: "error", Message: "invalid_argument: malformed process CLI request"}})
		return
	}
	encoded, err := json.Marshal(serveContractCLIRequest(req.Context(), request))
	if err != nil {
		writeProcessMCPResponse(w, http.StatusOK, processMCPResponse{Error: newProcessLinkError(ContractSystemError(err))})
		return
	}
	writeProcessMCPResponse(w, http.StatusOK, processMCPResponse{Result: encoded})
}

// serveCLI forwards one live CLI request to the binding's owning process in
// the current generation.
func (h *processHost) serveCLI(w http.ResponseWriter, req *http.Request) {
	var request ProcessHostCLIRequest
	if err := decodeProcessLinkBody(req, &request); err != nil || validateContractCLIRequest(request.Request) != nil {
		writeProcessLinkResponse(w, http.StatusBadRequest, processLinkResponse{Error: &processLinkError{Kind: "error", Message: "invalid_argument: malformed live CLI request"}})
		return
	}
	result, err := h.invokeCLI(req.Context(), request.Request, request.Generation)
	if err != nil {
		writeProcessLinkResponse(w, http.StatusOK, processLinkResponse{Error: newProcessLinkError(err)})
		return
	}
	writeProcessHostJSON(w, http.StatusOK, result)
}

func (h *processHost) invokeCLI(ctx context.Context, request ContractCLIRequest, expectedGeneration uint64) (ProcessHostCLIResult, error) {
	if expectedGeneration == 0 {
		return ProcessHostCLIResult{}, &errs.Error{Code: errs.InvalidArgument, Message: "a verified generation is required for live CLI invocation"}
	}
	generation := h.acquire(expectedGeneration)
	if generation == nil {
		return ProcessHostCLIResult{}, &errs.Error{Code: errs.Unavailable, Message: "application generation is not published"}
	}
	number, identity := generation.number, generation.identity
	process := generation.bindings[strings.TrimSpace(request.Binding)]
	defer generation.inFlight.Add(-1)
	if process == "" {
		return ProcessHostCLIResult{}, &errs.Error{Code: errs.NotFound, Message: fmt.Sprintf("contract CLI binding %s is not served by a service process of generation %d", request.Binding, number)}
	}
	body, err := json.Marshal(request)
	if err != nil {
		return ProcessHostCLIResult{}, ContractSystemError(err)
	}
	answer, err := h.callProcessIn(ctx, number, process, processCLICallPath, body)
	if err != nil {
		return ProcessHostCLIResult{}, err
	}
	var response ContractCLIResponse
	if err := json.Unmarshal(answer.Result, &response); err != nil {
		return ProcessHostCLIResult{}, ContractSystemError(fmt.Errorf("service process %s answered a malformed CLI response: %w", process, err))
	}
	return ProcessHostCLIResult{
		Response: response, Generation: number, Process: process, ContractRevision: identity.ContractRevision,
		ImplementationRevision: identity.ImplementationRevision, BuildInputDigest: identity.BuildInputDigest,
	}, nil
}
