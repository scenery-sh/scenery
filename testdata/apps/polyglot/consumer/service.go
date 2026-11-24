package consumer

import (
	"context"
	"fmt"

	contract "example.test/polyglot/consumer/scenerycontract"
	"scenery.sh"
)

type Service struct {
	quote contract.QuoteInternalClient
}

func NewService(_ context.Context, input contract.ConsumerConstructorInput) (*Service, error) {
	return &Service{quote: input.Clients.Quote}, nil
}

func (s *Service) Delegate(ctx context.Context, input contract.DelegateInput) (contract.DelegateOutcome, error) {
	invocation, ok := scenery.InvocationFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("invocation is unavailable")
	}
	reply, err := s.quote.Invoke(ctx, invocation, contract.QuoteClientInput{Amount: input.Amount})
	if err != nil {
		return nil, err
	}
	quote, ok := reply.(contract.QuoteClientOk)
	if !ok {
		return nil, fmt.Errorf("quote returned %T", reply)
	}
	return contract.DelegateOk{Value: contract.DelegateResult{Amount: quote.Value.Amount, Vendor: quote.Value.Vendor}}, nil
}
