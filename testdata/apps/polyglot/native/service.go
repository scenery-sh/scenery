package native

import (
	"context"

	contract "example.test/polyglot/native/scenerycontract"
)

type Service struct{}

func NewService(context.Context, contract.NativeConstructorInput) (*Service, error) {
	return &Service{}, nil
}

func (*Service) Label(context.Context, contract.LabelInput) (contract.LabelOutcome, error) {
	return contract.LabelOk{Value: contract.LabelResult{Vendor: "native"}}, nil
}
