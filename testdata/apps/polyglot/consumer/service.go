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
	switch quote := reply.(type) {
	case contract.QuoteClientOk:
		return contract.DelegateOk{Value: contract.DelegateResult{
			Amount: quote.Value.Amount, Vendor: quote.Value.Vendor, Principal: quote.Value.Principal,
			Tenant: quote.Value.Tenant, TraceId: quote.Value.TraceId, Deadline: quote.Value.Deadline, Currency: quote.Value.Currency,
		}}, nil
	case contract.QuoteClientNegativeAmount:
		return contract.DelegateNegativeAmount{Problem: quote.Problem}, nil
	default:
		return nil, fmt.Errorf("quote returned %T", reply)
	}
}
