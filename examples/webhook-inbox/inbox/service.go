package inbox

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"

	contract "example.com/webhook-inbox/inbox/scenerycontract"
	"scenery.sh/datasource"
)

type Service struct{ database datasource.SQL }

func NewService(_ context.Context, input contract.InboxConstructorInput) (*Service, error) {
	return &Service{database: input.Dependencies.Database}, nil
}

// Process is invoked by Scenery's durable worker, never by the admission request.
// Repeated deliveries retain the first event rather than overwrite its payload.
func (s *Service) Process(ctx context.Context, input contract.ProcessInput) (contract.ProcessOutcome, error) {
	digest := sha256.Sum256([]byte(input.Payload))
	value := contract.ProcessedEvent{EventId: input.EventId, Payload: input.Payload, Sha256: hex.EncodeToString(digest[:])}
	_, err := s.database.ExecContext(ctx,
		`INSERT INTO processed_events (event_id, payload, sha256) VALUES ($1, $2, $3) ON CONFLICT (event_id) DO NOTHING`,
		value.EventId, value.Payload, value.Sha256)
	if err != nil {
		return nil, err
	}
	// Read back the winning row so a retry after the queue's deduplication window
	// returns the original result even when its payload differs.
	if err := s.read(ctx, input.EventId, &value); err != nil {
		return nil, err
	}
	return contract.ProcessProcessed{Value: value}, nil
}

// Status reports completed business work. Missing means unprocessed or unknown,
// not proof that a previously accepted durable job was lost.
func (s *Service) Status(ctx context.Context, input contract.StatusInput) (contract.StatusOutcome, error) {
	var value contract.ProcessedEvent
	err := s.read(ctx, input.EventId, &value)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.StatusMissing{Value: contract.Lookup{EventId: input.EventId}}, nil
	}
	if err != nil {
		return nil, err
	}
	return contract.StatusProcessed{Value: value}, nil
}

func (s *Service) read(ctx context.Context, id string, value *contract.ProcessedEvent) error {
	return s.database.QueryRowContext(ctx, `SELECT event_id, payload, sha256 FROM processed_events WHERE event_id = $1`, id).
		Scan(&value.EventId, &value.Payload, &value.Sha256)
}
