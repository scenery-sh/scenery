package native

import (
	"context"
	"time"

	contract "example.test/polyglot/native/scenerycontract"
	"scenery.sh"
	"scenery.sh/datasource"
)

type Service struct{ database datasource.SQL }

func NewService(_ context.Context, input contract.NativeConstructorInput) (*Service, error) {
	return &Service{database: input.Dependencies.Database}, nil
}

func (*Service) Label(ctx context.Context, _ contract.LabelInput) (contract.LabelOutcome, error) {
	result := contract.LabelResult{Vendor: "native"}
	if call, ok := scenery.InvocationFromContext(ctx); ok && call.Principal() != "" {
		result.Principal = scenery.Some(call.Principal())
		result.Tenant = scenery.Some(call.TenantID())
		result.TraceId = scenery.Some(call.TraceID())
		if deadline, ok := call.Deadline(); ok {
			result.Deadline = scenery.Some(deadline.UTC().Format(time.RFC3339Nano))
		}
	}
	return contract.LabelOk{Value: result}, nil
}

func (s *Service) ReadDatabase(ctx context.Context, _ contract.ReadDatabaseInput) (contract.ReadDatabaseOutcome, error) {
	var vendor string
	// The fixture can hold this lock from a separate connection to observe a
	// request that is running when the client disconnects or the worker exits.
	if err := s.database.QueryRowContext(ctx, `SELECT 'postgres'::text FROM pg_advisory_xact_lock(467308)`).Scan(&vendor); err != nil {
		return nil, err
	}
	return contract.ReadDatabaseOk{Value: contract.LabelResult{Vendor: vendor}}, nil
}
