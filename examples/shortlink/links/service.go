package links

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	contract "example.com/shortlink/links/scenerycontract"
	"scenery.sh"
	"scenery.sh/datasource"
)

const schemaDDL = `
CREATE SCHEMA IF NOT EXISTS shortlink;

CREATE TABLE IF NOT EXISTS shortlink.links (
	slug        TEXT PRIMARY KEY,
	target_url  TEXT NOT NULL,
	host        TEXT NOT NULL,
	title       TEXT,
	clicks      BIGINT NOT NULL DEFAULT 0,
	created_by  TEXT NOT NULL DEFAULT '',
	created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	enriched_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS shortlink.clicks (
	id         BIGSERIAL PRIMARY KEY,
	click_id   TEXT NOT NULL UNIQUE,
	slug       TEXT NOT NULL REFERENCES shortlink.links(slug) ON DELETE CASCADE,
	target_url TEXT NOT NULL,
	clicked_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`

// Service manages short links in PostgreSQL and emits link lifecycle events.
type Service struct {
	database datasource.SQL
	guard    contract.GuardInternalClient
	created  contract.CreatedEventPublisher
	clicked  contract.ClickedEventPublisher
}

// NewService initializes the database schema and wires generated service handles.
func NewService(ctx context.Context, input contract.LinksConstructorInput) (*Service, error) {
	if _, err := input.Dependencies.Database.ExecContext(ctx, schemaDDL); err != nil {
		return nil, fmt.Errorf("init shortlink schema: %w", err)
	}
	return &Service{
		database: input.Dependencies.Database,
		guard:    input.Clients.Guard,
		created:  input.Publishers.Created,
		clicked:  input.Publishers.Clicked,
	}, nil
}

// CreateLink validates the target URL via the Rust guard service, persists the link,
// and emits a transactional link_created event.
func (s *Service) CreateLink(ctx context.Context, input contract.CreateLinkInput) (contract.CreateLinkOutcome, error) {
	call, _ := scenery.InvocationFromContext(ctx)
	reply, err := s.guard.Invoke(ctx, call, contract.GuardClientInput(input))
	if err != nil {
		return nil, fmt.Errorf("validate url via guard: %w", err)
	}

	var validated contract.GuardClientOk
	switch outcome := reply.(type) {
	case contract.GuardClientOk:
		validated = outcome
	case contract.GuardClientInvalidUrl:
		return contract.CreateLinkInvalidUrl(outcome), nil
	default:
		return nil, fmt.Errorf("unexpected guard outcome: %T", reply)
	}

	createdBy := "anonymous"
	if call.Principal() != "" {
		createdBy = call.Principal()
	}

	now := time.Now().UTC()
	res, err := s.database.ExecContext(
		ctx,
		`INSERT INTO shortlink.links (slug, target_url, host, created_by, created_at)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (slug) DO NOTHING`,
		validated.Value.Slug,
		validated.Value.NormalizedUrl,
		validated.Value.Host,
		createdBy,
		now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert link: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return contract.CreateLinkSlugTaken{
			Problem: scenery.Problem{
				Code:    "slug_taken",
				Message: fmt.Sprintf("Slug %q is already taken.", validated.Value.Slug),
			},
		}, nil
	}

	if _, err := s.created.Emit(ctx, contract.LinkCreatedEvent{
		Slug:      validated.Value.Slug,
		TargetUrl: validated.Value.NormalizedUrl,
		CreatedBy: createdBy,
	}); err != nil {
		return nil, fmt.Errorf("emit link_created: %w", err)
	}

	return contract.CreateLinkCreated{
		Value: contract.LinkItem{
			Slug:      validated.Value.Slug,
			TargetUrl: validated.Value.NormalizedUrl,
			Host:      validated.Value.Host,
			Clicks:    0,
			CreatedAt: now.Format(time.RFC3339),
		},
	}, nil
}

// ListLinks returns the newest links along with click counts and enriched titles.
func (s *Service) ListLinks(ctx context.Context, _ contract.ListLinksInput) (contract.ListLinksOutcome, error) {
	rows, err := s.database.QueryContext(
		ctx,
		`SELECT slug, target_url, host, title, clicks, created_at, enriched_at
		 FROM shortlink.links
		 ORDER BY created_at DESC
		 LIMIT 100`,
	)
	if err != nil {
		return nil, fmt.Errorf("query links: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]contract.LinkItem, 0)
	for rows.Next() {
		var (
			slug       string
			targetURL  string
			host       string
			title      sql.NullString
			clicks     int64
			createdAt  time.Time
			enrichedAt sql.NullTime
		)
		if err := rows.Scan(&slug, &targetURL, &host, &title, &clicks, &createdAt, &enrichedAt); err != nil {
			return nil, fmt.Errorf("scan link: %w", err)
		}
		item := contract.LinkItem{
			Slug:      slug,
			TargetUrl: targetURL,
			Host:      host,
			Clicks:    clicks,
			CreatedAt: createdAt.UTC().Format(time.RFC3339),
		}
		if title.Valid {
			item.Title = scenery.Some(title.String)
		}
		if enrichedAt.Valid {
			item.EnrichedAt = scenery.Some(enrichedAt.Time.UTC().Format(time.RFC3339))
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate links: %w", err)
	}
	return contract.ListLinksOk{Value: contract.ListLinksResult{Items: items}}, nil
}

// ResolveLink increments the click count, records the click, emits link_clicked,
// and returns the destination URL for an HTTP 302 redirect.
func (s *Service) ResolveLink(ctx context.Context, input contract.ResolveLinkInput) (contract.ResolveLinkOutcome, error) {
	slug := strings.TrimSpace(input.Slug)
	var targetURL string
	err := s.database.QueryRowContext(
		ctx,
		`UPDATE shortlink.links
		 SET clicks = clicks + 1
		 WHERE slug = $1
		 RETURNING target_url`,
		slug,
	).Scan(&targetURL)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.ResolveLinkNotFound{
			Problem: scenery.Problem{
				Code:    "not_found",
				Message: fmt.Sprintf("Short link %q was not found.", slug),
			},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve link: %w", err)
	}

	clickID := newClickID()
	clickedAt := time.Now().UTC()
	if _, err := s.database.ExecContext(
		ctx,
		`INSERT INTO shortlink.clicks (click_id, slug, target_url, clicked_at)
		 VALUES ($1, $2, $3, $4)`,
		clickID,
		slug,
		targetURL,
		clickedAt,
	); err != nil {
		return nil, fmt.Errorf("record click: %w", err)
	}

	if _, err := s.clicked.Emit(ctx, contract.LinkClickedEvent{
		ClickId:   clickID,
		Slug:      slug,
		TargetUrl: targetURL,
		ClickedAt: clickedAt.Format(time.RFC3339Nano),
	}); err != nil {
		return nil, fmt.Errorf("emit link_clicked: %w", err)
	}

	return contract.ResolveLinkOk{
		Value: targetURL,
	}, nil
}

// DeleteLink removes a short link and its click history.
func (s *Service) DeleteLink(ctx context.Context, input contract.DeleteLinkInput) (contract.DeleteLinkOutcome, error) {
	res, err := s.database.ExecContext(
		ctx,
		`DELETE FROM shortlink.links WHERE slug = $1`,
		strings.TrimSpace(input.Slug),
	)
	if err != nil {
		return nil, fmt.Errorf("delete link: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return contract.DeleteLinkNotFound{
			Problem: scenery.Problem{
				Code:    "not_found",
				Message: fmt.Sprintf("Short link %q was not found.", input.Slug),
			},
		}, nil
	}
	return contract.DeleteLinkDeleted{}, nil
}

func newClickID() string {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("clk_%d", time.Now().UnixNano())
	}
	return "clk_" + hex.EncodeToString(buf[:])
}
