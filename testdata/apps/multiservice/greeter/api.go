package greeter

import (
	"context"
	"encoding/json"
	"fmt"

	greetercontract "example.com/multiservice/greeter/scenerycontract"
	sceneryruntime "scenery.sh/runtime"
)

type Service struct{}

func NewService(context.Context, greetercontract.GreeterConstructorInput) (*Service, error) {
	return &Service{}, nil
}

// Greet crosses the service boundary through echo's internal binding.
func (*Service) Greet(ctx context.Context, input greetercontract.GreetInput) (greetercontract.GreetOutcome, error) {
	request, err := json.Marshal(map[string]string{"message": "hello " + input.Name})
	if err != nil {
		return nil, err
	}
	response, err := sceneryruntime.InvokeContractBindingJSON(ctx, "echo/binding/echo_internal", "greeter", request)
	if err != nil {
		return nil, fmt.Errorf("invoke echo: %w", err)
	}
	var outcome struct {
		Kind  string `json:"kind"`
		Name  string `json:"name"`
		Value struct {
			Message string `json:"message"`
		} `json:"value"`
	}
	if err := json.Unmarshal(response, &outcome); err != nil {
		return nil, err
	}
	if outcome.Kind != "result" || outcome.Name != "ok" {
		return nil, fmt.Errorf("echo returned %s %s", outcome.Kind, outcome.Name)
	}
	return greetercontract.GreetOk{Value: greetercontract.GreetResult{Message: "greeter:" + outcome.Value.Message}}, nil
}
