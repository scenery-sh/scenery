package echo

import (
	"context"

	echocontract "example.com/multiservice/echo/scenerycontract"
)

type Service struct{}

func NewService(context.Context, echocontract.EchoConstructorInput) (*Service, error) {
	return &Service{}, nil
}

func (*Service) Echo(_ context.Context, input echocontract.EchoInput) (echocontract.EchoOutcome, error) {
	return echocontract.EchoOk{Value: echocontract.EchoResult{Message: "echo:" + input.Message}}, nil
}
