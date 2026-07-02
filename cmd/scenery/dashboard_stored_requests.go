package main

import (
	"context"

	"scenery.sh/internal/devdash"
)

type storedRequestRPCInput struct {
	Title  string                    `json:"title"`
	RPC    string                    `json:"rpcName"`
	Svc    string                    `json:"svcName"`
	Shared bool                      `json:"shared"`
	Data   devdash.StoredRequestData `json:"data"`
}

type storedRequestRPCParams struct {
	AppID string                `json:"app_id"`
	ID    string                `json:"id"`
	Input storedRequestRPCInput `json:"input"`
}

func (s *dashboardServer) listStoredRequests(ctx context.Context, appID string) ([]devdash.StoredRequest, error) {
	return s.dashboardStore().ListStoredRequests(ctx, appID)
}

func (s *dashboardServer) createStoredRequest(ctx context.Context, params storedRequestRPCParams) (devdash.StoredRequest, error) {
	return s.dashboardStore().CreateStoredRequest(ctx, devdash.StoredRequest{
		AppID:  firstNonEmpty(params.AppID, s.dashboardActiveAppID()),
		Title:  params.Input.Title,
		RPC:    params.Input.RPC,
		Svc:    params.Input.Svc,
		Shared: params.Input.Shared,
		Data:   params.Input.Data,
	})
}

func (s *dashboardServer) updateStoredRequest(ctx context.Context, params storedRequestRPCParams) (devdash.StoredRequest, error) {
	return s.dashboardStore().UpdateStoredRequest(ctx, devdash.StoredRequest{
		ID:     params.ID,
		AppID:  firstNonEmpty(params.AppID, s.dashboardActiveAppID()),
		Title:  params.Input.Title,
		RPC:    params.Input.RPC,
		Svc:    params.Input.Svc,
		Shared: params.Input.Shared,
		Data:   params.Input.Data,
	})
}

func (s *dashboardServer) deleteStoredRequest(ctx context.Context, appID, id string) error {
	return s.dashboardStore().DeleteStoredRequest(ctx, appID, id)
}
