package native

import (
	"context"
	"fmt"
	"io"
	"strings"
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

func (s *Service) CheckDatabase(ctx context.Context, _ contract.CheckDatabaseInput) (contract.CheckDatabaseOutcome, error) {
	var vendor string
	if err := s.database.QueryRowContext(ctx, `SELECT 'postgres'::text`).Scan(&vendor); err != nil {
		return nil, err
	}
	return contract.CheckDatabaseOk{Value: contract.LabelResult{Vendor: vendor}}, nil
}

// Download returns the same deterministic size as the Rust streaming fixture.
func (*Service) Download(_ context.Context, _ contract.DownloadInput) (contract.DownloadOutcome, scenery.ByteStream, error) {
	body := strings.NewReader(strings.Repeat("g", 64<<10))
	return contract.DownloadOk{Value: []byte{}}, scenery.NewByteStream(io.NopCloser(body), body.Size()), nil
}

func (*Service) ReceiveOrder(ctx context.Context, input contract.ReceiveOrderInput) (contract.ReceiveOrderOutcome, error) {
	call, ok := scenery.InvocationFromContext(ctx)
	if !ok || input.Amount != 9007199254740993 {
		return nil, fmt.Errorf("missing invocation or incorrect event amount")
	}
	return contract.ReceiveOrderOk{Value: call.TraceID()}, nil
}
