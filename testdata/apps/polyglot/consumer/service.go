package consumer

import (
	"context"
	"fmt"

	contract "example.test/polyglot/consumer/scenerycontract"
	"scenery.sh"
	"scenery.sh/errs"
)

type Service struct {
	accepted   contract.AcceptedEventPublisher
	quote      contract.QuoteInternalClient
	typescript contract.TypescriptInternalClient
}

func NewService(_ context.Context, input contract.ConsumerConstructorInput) (*Service, error) {
	return &Service{accepted: input.Publishers.Accepted, quote: input.Clients.Quote, typescript: input.Clients.Typescript}, nil
}

func (s *Service) Delegate(ctx context.Context, input contract.DelegateInput) (contract.DelegateOutcome, error) {
	invocation, ok := scenery.InvocationFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("invocation is unavailable")
	}
	if input.Typescript.Set && input.Typescript.Value {
		reply, err := s.typescript.Invoke(ctx, invocation, contract.TypescriptClientInput{Amount: input.Amount})
		if err != nil {
			return nil, err
		}
		switch quote := reply.(type) {
		case contract.TypescriptClientOk:
			return contract.DelegateOk{Value: contract.DelegateResult{
				Amount: quote.Value.Amount, Vendor: quote.Value.Vendor, Principal: quote.Value.Principal,
				Tenant: quote.Value.Tenant, TraceId: quote.Value.TraceId, Deadline: quote.Value.Deadline, Currency: quote.Value.Currency,
			}}, nil
		case contract.TypescriptClientNegativeAmount:
			return contract.DelegateNegativeAmount{Problem: quote.Problem}, nil
		default:
			return nil, fmt.Errorf("TypeScript quote returned %T", reply)
		}
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

func (s *Service) PublishOrder(ctx context.Context, input contract.PublishOrderInput) (contract.PublishOrderOutcome, error) {
	receipt, err := s.accepted.Emit(ctx, input)
	if errs.Code(err) == errs.PermissionDenied {
		return contract.PublishOrderDenied{Problem: scenery.Problem{Code: "order_denied", Message: "Order was denied."}}, nil
	}
	if err != nil {
		return nil, err
	}
	return contract.PublishOrderOk{Value: contract.PublishResult{
		EventId: receipt.EventID, Event: receipt.Event, Deliveries: int32(receipt.Deliveries), Deduplicated: receipt.Deduplicated,
	}}, nil
}
