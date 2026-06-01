package service

import "context"

type PingResponse struct {
	Message string `json:"message"`
}

//onlava:api public path=/script-fixture/ping method=GET
func Ping(ctx context.Context) (*PingResponse, error) {
	return &PingResponse{Message: "ok"}, nil
}
