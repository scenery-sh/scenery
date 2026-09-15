package greeter

import (
	"context"
	"fmt"

	echocontract "example.com/multiservice/echo/scenerycontract"
	greetercontract "example.com/multiservice/greeter/scenerycontract"
	"example.com/multiservice/internal/text"
	"scenery.sh"
)

type Service struct {
	echo greetercontract.EchoInternalClient
}

func NewService(_ context.Context, input greetercontract.GreeterConstructorInput) (*Service, error) {
	return &Service{echo: input.Clients.Echo}, nil
}

// Greet crosses the service boundary through echo's typed internal client.
func (s *Service) Greet(ctx context.Context, input greetercontract.GreetInput) (greetercontract.GreetOutcome, error) {
	invocation, ok := scenery.InvocationFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("greet requires a runtime invocation")
	}
	outcome, err := s.echo.Invoke(ctx, invocation, echocontract.EchoInput{Message: "hello " + input.Name})
	if err != nil {
		return nil, fmt.Errorf("invoke echo: %w", err)
	}
	result, ok := outcome.(echocontract.EchoOk)
	if !ok {
		return nil, fmt.Errorf("echo returned %T", outcome)
	}
	return greetercontract.GreetOk{Value: greetercontract.GreetResult{Message: text.Label("greeter", result.Value.Message)}}, nil
}
