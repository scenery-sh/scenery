package greeter

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	// A name of the form wait:<duration>:<name> delays the call to echo, so the
	// process-model probe can keep a request in flight while echo is replaced.
	name := input.Name
	if rest, found := strings.CutPrefix(name, "wait:"); found {
		delay, remainder, _ := strings.Cut(rest, ":")
		duration, err := time.ParseDuration(delay)
		if err != nil {
			return nil, fmt.Errorf("invalid wait: %w", err)
		}
		select {
		case <-time.After(duration):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		name = remainder
	}
	outcome, err := s.echo.Invoke(ctx, invocation, echocontract.EchoInput{Message: "hello " + name})
	if err != nil {
		return nil, fmt.Errorf("invoke echo: %w", err)
	}
	result, ok := outcome.(echocontract.EchoOk)
	if !ok {
		return nil, fmt.Errorf("echo returned %T", outcome)
	}
	return greetercontract.GreetOk{Value: greetercontract.GreetResult{Message: text.Label("greeter", result.Value.Message)}}, nil
}
