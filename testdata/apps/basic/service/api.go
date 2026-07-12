package service

import (
	"context"

	servicecontract "example.com/basicapp/service/scenerycontract"
)

type Service struct{}

func NewService(context.Context, servicecontract.ServiceConstructorInput) (*Service, error) {
	return &Service{}, nil
}

func (*Service) Echo(_ context.Context, input servicecontract.EchoInput) (servicecontract.EchoOutcome, error) {
	return servicecontract.EchoOk{Value: servicecontract.EchoResult{Message: "echo:" + input.Message}}, nil
}
