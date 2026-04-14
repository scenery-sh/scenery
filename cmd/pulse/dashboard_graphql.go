package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"pulse.dev/internal/devdash"
)

type graphqlRequest struct {
	OperationName string          `json:"operationName"`
	Query         string          `json:"query"`
	Variables     json.RawMessage `json:"variables"`
}

type storedRequestInput struct {
	Title  string                    `json:"title"`
	RPC    string                    `json:"rpcName"`
	Svc    string                    `json:"svcName"`
	Shared bool                      `json:"shared"`
	Data   devdash.StoredRequestData `json:"data"`
}

type graphqlErrorBody struct {
	Errors []graphqlErrorItem `json:"errors"`
}

type graphqlErrorItem struct {
	Message string `json:"message"`
}

func (s *dashboardServer) handleGraphQL(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var gqlReq graphqlRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&gqlReq); err != nil {
		writeGraphQLJSON(w, http.StatusBadRequest, graphqlErrorBody{
			Errors: []graphqlErrorItem{{Message: "invalid GraphQL request body"}},
		})
		return
	}

	op := firstNonEmpty(strings.TrimSpace(gqlReq.OperationName), inferGraphQLOperation(gqlReq.Query))
	if op == "" {
		writeGraphQLJSON(w, http.StatusOK, graphqlErrorBody{
			Errors: []graphqlErrorItem{{Message: "missing GraphQL operationName"}},
		})
		return
	}

	data, err := s.dispatchGraphQL(req.Context(), op, gqlReq.Variables)
	if err != nil {
		status := http.StatusOK
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		writeGraphQLJSON(w, status, graphqlErrorBody{
			Errors: []graphqlErrorItem{{Message: err.Error()}},
		})
		return
	}

	writeGraphQLJSON(w, http.StatusOK, map[string]any{"data": data})
}

func (s *dashboardServer) dispatchGraphQL(ctx context.Context, operation string, raw json.RawMessage) (any, error) {
	switch operation {
	case "getStoredRequests":
		var params struct {
			AppSlug string `json:"appSlug"`
		}
		if err := decodeGraphQLVariables(raw, &params); err != nil {
			return nil, err
		}
		appID := firstNonEmpty(params.AppSlug, s.supervisor.activeAppID())
		requests, err := s.supervisor.store.ListStoredRequests(ctx, appID)
		if err != nil {
			return nil, err
		}
		items := make([]map[string]any, 0, len(requests))
		for _, req := range requests {
			items = append(items, storedRequestGraphQL(req))
		}
		return map[string]any{
			"app": map[string]any{
				"__typename":     "App",
				"storedRequests": items,
			},
		}, nil
	case "createStoredRequest":
		var params struct {
			AppSlug string             `json:"appSlug"`
			Input   storedRequestInput `json:"input"`
		}
		if err := decodeGraphQLVariables(raw, &params); err != nil {
			return nil, err
		}
		created, err := s.supervisor.store.CreateStoredRequest(ctx, devdash.StoredRequest{
			AppID:  firstNonEmpty(params.AppSlug, s.supervisor.activeAppID()),
			Title:  params.Input.Title,
			RPC:    params.Input.RPC,
			Svc:    params.Input.Svc,
			Shared: params.Input.Shared,
			Data:   params.Input.Data,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"app": map[string]any{
				"__typename": "App",
				"createStoredRequest": map[string]any{
					"__typename": "StoredRequest",
					"id":         created.ID,
				},
			},
		}, nil
	case "updateStoredRequest":
		var params struct {
			AppSlug         string             `json:"appSlug"`
			StoredRequestID string             `json:"storedRequestID"`
			Input           storedRequestInput `json:"input"`
		}
		if err := decodeGraphQLVariables(raw, &params); err != nil {
			return nil, err
		}
		updated, err := s.supervisor.store.UpdateStoredRequest(ctx, devdash.StoredRequest{
			ID:     params.StoredRequestID,
			AppID:  firstNonEmpty(params.AppSlug, s.supervisor.activeAppID()),
			Title:  params.Input.Title,
			RPC:    params.Input.RPC,
			Svc:    params.Input.Svc,
			Shared: params.Input.Shared,
			Data:   params.Input.Data,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"app": map[string]any{
				"__typename": "App",
				"storedRequest": map[string]any{
					"__typename": "StoredRequest",
					"update": map[string]any{
						"__typename": "StoredRequest",
						"id":         updated.ID,
					},
				},
			},
		}, nil
	case "deleteStoredRequest":
		var params struct {
			AppSlug         string `json:"appSlug"`
			StoredRequestID string `json:"storedRequestID"`
		}
		if err := decodeGraphQLVariables(raw, &params); err != nil {
			return nil, err
		}
		if err := s.supervisor.store.DeleteStoredRequest(ctx, firstNonEmpty(params.AppSlug, s.supervisor.activeAppID()), params.StoredRequestID); err != nil {
			return nil, err
		}
		return map[string]any{
			"app": map[string]any{
				"__typename": "App",
				"storedRequest": map[string]any{
					"__typename": "StoredRequest",
					"delete":     true,
				},
			},
		}, nil
	default:
		return nil, errors.New("unsupported in Pulse dashboard local GraphQL: " + operation)
	}
}

func decodeGraphQLVariables(raw json.RawMessage, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	return json.Unmarshal(raw, dst)
}

func inferGraphQLOperation(query string) string {
	for _, op := range []string{
		"getStoredRequests",
		"createStoredRequest",
		"updateStoredRequest",
		"deleteStoredRequest",
	} {
		if strings.Contains(query, op) {
			return op
		}
	}
	return ""
}

func storedRequestGraphQL(req devdash.StoredRequest) map[string]any {
	return map[string]any{
		"__typename": "StoredRequest",
		"id":         req.ID,
		"title":      req.Title,
		"rpcName":    req.RPC,
		"svcName":    req.Svc,
		"shared":     req.Shared,
		"data": map[string]any{
			"__typename": "StoredRequestData",
			"method":     req.Data.Method,
			"pathParams": decodeStoredRequestJSON(req.Data.PathParams),
			"payload":    decodeStoredRequestJSON(req.Data.Payload),
		},
	}
}

func decodeStoredRequestJSON(raw json.RawMessage) any {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return value
}

func writeGraphQLJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
