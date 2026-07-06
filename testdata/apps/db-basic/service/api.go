package service

import "context"

//scenery:service
type Service struct{}

func initService() (*Service, error) { return &Service{}, nil }

type PingResponse struct {
	Message string `json:"message"`
}

//scenery:api public
func (s *Service) Ping(ctx context.Context) (*PingResponse, error) {
	return &PingResponse{Message: "pong"}, nil
}
