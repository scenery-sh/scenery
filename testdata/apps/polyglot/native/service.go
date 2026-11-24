package native

import (
	"context"

	contract "example.test/polyglot/native/scenerycontract"
	"scenery.sh/datasource"
)

type Service struct{ database datasource.SQL }

func NewService(_ context.Context, input contract.NativeConstructorInput) (*Service, error) {
	return &Service{database: input.Dependencies.Database}, nil
}

func (*Service) Label(context.Context, contract.LabelInput) (contract.LabelOutcome, error) {
	return contract.LabelOk{Value: contract.LabelResult{Vendor: "native"}}, nil
}

func (s *Service) ReadDatabase(ctx context.Context, _ contract.ReadDatabaseInput) (contract.ReadDatabaseOutcome, error) {
	var vendor string
	if err := s.database.QueryRowContext(ctx, `SELECT 'postgres'::text`).Scan(&vendor); err != nil {
		return nil, err
	}
	return contract.ReadDatabaseOk{Value: contract.LabelResult{Vendor: vendor}}, nil
}
