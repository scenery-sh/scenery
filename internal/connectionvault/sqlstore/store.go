// Package sqlstore adapts framework-owned auth SQL to the provider-neutral vault.
package sqlstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/internal/connectionvault"
)

// Store opens the existing standard-auth pool lazily. It owns no second pool,
// configuration source, bootstrap or automatic schema migration.
type Store struct {
	Open func(context.Context) (*sql.DB, error)
}

type lease struct {
	conn          *sql.Conn
	tx            *sql.Tx
	query         *authdb.Queries
	user          authdb.UUID
	provider, key string
}

func parseID(raw string) (authdb.UUID, error) {
	id, err := uuid.Parse(raw)
	return authdb.UUID{Bytes: [16]byte(id), Valid: err == nil}, err
}
func idString(id authdb.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
func nullableTime(value time.Time) sql.NullTime {
	return sql.NullTime{Time: value, Valid: !value.IsZero()}
}

func discard(conn *sql.Conn) { _ = conn.Raw(func(any) error { return driver.ErrBadConn }) }

func (s Store) Lock(ctx context.Context, user, provider string) (connectionvault.Lease, error) {
	parsed, err := parseID(user)
	if err != nil {
		return nil, connectionvault.ErrOwnerUnavailable
	}
	pool, err := s.Open(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		return nil, err
	}
	l := &lease{conn: conn, user: parsed, provider: provider, key: "scenery.connection:" + idString(parsed) + ":" + provider}
	// A session lease spans the durable refresh checkpoint; each transaction
	// still locks the actual connection row FOR UPDATE. Context bounds waiting.
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, l.key); err != nil {
		discard(conn)
		_ = conn.Close()
		return nil, err
	}
	if err := l.begin(ctx); err != nil {
		_ = l.Close()
		return nil, err
	}
	return l, nil
}

func (l *lease) begin(ctx context.Context) error {
	tx, err := l.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	l.tx, l.query = tx, authdb.New(tx)
	// Disabled/deleted owners cannot obtain credentials. The share lock also
	// prevents an owner lifecycle update during this transaction's issuance.
	var disabled sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT disabled_at FROM scenery.scenery_auth_users WHERE id=$1 FOR SHARE`, l.user).Scan(&disabled)
	if errors.Is(err, sql.ErrNoRows) || err == nil && disabled.Valid {
		return connectionvault.ErrOwnerUnavailable
	}
	return err
}

func (l *lease) Load(ctx context.Context) (connectionvault.Connection, error) {
	row, err := l.query.GetConnectionByUserForUpdate(ctx, authdb.GetConnectionByUserForUpdateParams{UserID: l.user, ProviderID: l.provider})
	if errors.Is(err, sql.ErrNoRows) {
		return connectionvault.Connection{}, connectionvault.ErrNotConnected
	}
	if err != nil {
		return connectionvault.Connection{}, err
	}
	scopes, err := connectionvault.ParseScopes(row.Scopes)
	if err != nil {
		return connectionvault.Connection{}, err
	}
	return connectionvault.Connection{
		ID: idString(row.ID), UserID: idString(row.UserID), ProviderID: row.ProviderID,
		Issuer: row.Issuer, Subject: row.ProviderSubject, Resource: row.Resource, TokenType: row.TokenType,
		Email: row.Email, Scopes: scopes, RefreshCiphertext: row.RefreshTokenCiphertext, AccessCiphertext: row.AccessTokenCiphertext,
		AccessExpiresAt: row.AccessTokenExpiresAt.Time, Status: row.Status, LastRefreshAt: row.LastRefreshAt.Time,
		LastRefreshError: row.LastRefreshError, RevocationStatus: row.RevocationStatus,
		ConnectedAt: row.ConnectedAt, DisconnectedAt: row.DisconnectedAt.Time, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

func (l *lease) Save(ctx context.Context, row connectionvault.Connection) error {
	if row.UserID != idString(l.user) || row.ProviderID != l.provider {
		return connectionvault.ErrOwnerUnavailable
	}
	if row.ID == "" {
		id, err := uuid.NewRandom()
		if err != nil {
			return err
		}
		row.ID = id.String()
	}
	id, err := parseID(row.ID)
	if err != nil {
		return err
	}
	_, err = l.query.SaveConnection(ctx, authdb.SaveConnectionParams{
		ID: id, UserID: l.user, ProviderID: row.ProviderID, Issuer: row.Issuer, ProviderSubject: row.Subject, Resource: row.Resource,
		TokenType: row.TokenType, Email: row.Email, Scopes: strings.Join(row.Scopes, " "), RefreshTokenCiphertext: row.RefreshCiphertext,
		AccessTokenCiphertext: row.AccessCiphertext, AccessTokenExpiresAt: nullableTime(row.AccessExpiresAt), Status: row.Status,
		LastRefreshAt: nullableTime(row.LastRefreshAt), LastRefreshError: row.LastRefreshError, RevocationStatus: row.RevocationStatus,
		ConnectedAt: row.ConnectedAt, DisconnectedAt: nullableTime(row.DisconnectedAt), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	})
	if err != nil {
		return err
	}
	return nil
}

func (l *lease) ExpireStates(ctx context.Context) error {
	return l.query.ExpireConnectionStates(ctx, authdb.ExpireConnectionStatesParams{UserID: l.user, ProviderID: l.provider})
}

func (l *lease) Checkpoint(ctx context.Context, row connectionvault.Connection) error {
	if err := l.Save(ctx, row); err != nil {
		return err
	}
	if err := l.Commit(); err != nil {
		return err
	}
	if err := l.begin(ctx); err != nil {
		return err
	}
	// Revalidate owner and lock the checkpointed row before remote work. The
	// session advisory lease keeps other vault writers serialized across commits.
	_, err := l.Load(ctx)
	return err
}

func (l *lease) Commit() error {
	if l.tx == nil {
		return sql.ErrTxDone
	}
	err := l.tx.Commit()
	l.tx = nil
	l.query = nil
	return err
}

func (l *lease) Close() error {
	if l.conn == nil {
		return nil
	}
	conn := l.conn
	l.conn = nil
	if l.tx != nil {
		err := l.tx.Rollback()
		l.tx = nil
		if err != nil && !errors.Is(err, sql.ErrTxDone) {
			discard(conn)
			_ = conn.Close()
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var unlocked bool
	err := conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, l.key).Scan(&unlocked)
	if err != nil || !unlocked {
		discard(conn)
		_ = conn.Close()
		if err != nil {
			return err
		}
		return errors.New("connection lease could not be released")
	}
	return conn.Close()
}
