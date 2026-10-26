package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"scenery.sh/errs"
	sceneryruntime "scenery.sh/runtime"
)

// runtimeLiveCLIResult is the `cli/invoke` result: the contract CLI response
// of the binding's owning process and the generation that served it.
type runtimeLiveCLIResult = sceneryruntime.ProcessHostCLIResult

// invokeLiveCLI sends one validated contract CLI request to the running
// process host, which serves it in the binding's owning service process of
// the current generation.
func (s *devSupervisor) invokeLiveCLI(ctx context.Context, request sceneryruntime.ProcessHostCLIRequest) (runtimeLiveCLIResult, error) {
	s.mu.RLock()
	model := s.processes
	s.mu.RUnlock()
	if model == nil {
		return runtimeLiveCLIResult{}, &errs.Error{Code: errs.Unavailable, Message: "the development runtime has no running process host"}
	}
	model.mu.Lock()
	link := model.link
	model.mu.Unlock()
	if link == nil {
		return runtimeLiveCLIResult{}, &errs.Error{Code: errs.Unavailable, Message: "the development runtime has no running process host"}
	}
	body, err := json.Marshal(request)
	if err != nil {
		return runtimeLiveCLIResult{}, err
	}
	call, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://scenery-host/__scenery/process/v1/cli", bytes.NewReader(body))
	if err != nil {
		return runtimeLiveCLIResult{}, err
	}
	call.Header.Set("Authorization", "Bearer "+link.token)
	call.Header.Set("Content-Type", "application/json")
	response, err := link.control.Do(call)
	if err != nil {
		return runtimeLiveCLIResult{}, &errs.Error{Code: errs.Unavailable, Message: "the process host did not answer the live CLI request", Cause: err}
	}
	defer func() { _ = response.Body.Close() }()
	var answer struct {
		runtimeLiveCLIResult
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&answer); err != nil {
		return runtimeLiveCLIResult{}, fmt.Errorf("decode the process host's live CLI answer (HTTP %d): %w", response.StatusCode, err)
	}
	if answer.Error != nil {
		code := errs.ErrCode(answer.Error.Code)
		if code == "" {
			code = errs.Unavailable
		}
		return runtimeLiveCLIResult{}, &errs.Error{Code: code, Message: answer.Error.Message}
	}
	if response.StatusCode != http.StatusOK {
		return runtimeLiveCLIResult{}, fmt.Errorf("the process host answered HTTP %d", response.StatusCode)
	}
	return answer.runtimeLiveCLIResult, nil
}
