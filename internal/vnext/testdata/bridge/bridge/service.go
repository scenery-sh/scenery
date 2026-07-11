package bridge

import "context"

//scenery:service
type Service struct{}

func initService() (*Service, error) { return &Service{}, nil }

type EchoParams struct {
	Message string `json:"message"`
}

type EchoResponse struct {
	Message string `json:"message"`
}

//scenery:api public path=/echo method=POST
func (service *Service) Echo(_ context.Context, params *EchoParams) (*EchoResponse, error) {
	return &EchoResponse{Message: "legacy:" + params.Message}, nil
}
