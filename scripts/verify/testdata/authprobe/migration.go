package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/auth"
	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
	"scenery.sh/internal/authschema"
	"scenery.sh/internal/connectionvault"
)

func (p *probe) migrationSource(relative string) string {
	if p.sourceRoot == "" {
		panic(failure{os.ErrNotExist})
	}
	return string(take(os.ReadFile(filepath.Join(p.sourceRoot, relative))))
}
func (p *probe) applyConnectionMigration() error {
	tx, err := p.db.BeginTx(p.ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(p.ctx, p.migrationSource("auth/db/migrations/0001-provider-neutral-connections.sql")); err != nil {
		return err
	}
	if err := authschema.CheckCurrentConnections(p.ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}
func (p *probe) installLegacyConnections() {
	_, err := p.db.ExecContext(p.ctx, `DROP TABLE scenery.scenery_auth_connection_states; DROP TABLE scenery.scenery_auth_connections; DROP TABLE scenery.scenery_auth_connection_schema`)
	must(err)
	_, err = p.db.ExecContext(p.ctx, p.migrationSource("auth/db/migrations/testdata/legacy-schema.sql"))
	must(err)
}
func (p *probe) unrelatedAuthSnapshot() string {
	var values []string
	for _, table := range []string{"scenery_auth_users", "scenery_auth_tenants", "scenery_auth_organization_memberships", "scenery_auth_auth_identities", "scenery_auth_refresh_sessions", "scenery_auth_auth_events", "scenery_auth_one_time_tokens"} {
		var snapshot string
		must(p.db.QueryRowContext(p.ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(value) ORDER BY id),'[]'::jsonb)::text FROM scenery.`+table+` AS value`).Scan(&snapshot))
		values = append(values, snapshot)
	}
	return strings.Join(values, "\n")
}

func connectionMigrationJourney(p *probe) {
	session, _ := p.signin()
	p.installLegacyConnections()
	cipher := connectionvault.TokenCipher{Key: func() ([]byte, error) { return []byte("12345678901234567890123456789012"), nil }}
	refresh := take(cipher.Seal("legacy-refresh"))
	access := take(cipher.Seal("legacy-access"))
	connectionID := newID()
	connected := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	_, err := p.db.ExecContext(p.ctx, `INSERT INTO scenery.scenery_auth_google_connections (id,user_id,provider_subject,email,scopes,refresh_token_ciphertext,access_token_ciphertext,access_token_expires_at,status,connected_at,created_at,updated_at) VALUES ($1,$2,'google-subject','person@example.test',$3,$4,$5,now()+interval '1 hour','active',$6,$6,$6)`, connectionID, id(session.User.ID), gmailModify, refresh, access, connected)
	must(err)
	loginState := newID()
	connectionState := newID()
	_, err = p.db.ExecContext(p.ctx, `INSERT INTO scenery.scenery_auth_oauth_states (id,state_hash,pkce_verifier,user_id,purpose,expires_at) VALUES ($1,'preserved-login-state','login-verifier',NULL,'login',now()+interval '1 hour'),($2,'retired-connection-state','connection-verifier',$3,'google_connection',now()+interval '1 hour')`, loginState, connectionState, id(session.User.ID))
	must(err)
	before := p.unrelatedAuthSnapshot()
	_, tokenErr := auth.ConnectionTokenForUser(p.ctx, session.User.ID, "google", gmailModify)
	p.check(errs.Code(tokenErr) == errs.ConnectionConfiguration, "new runtime refuses unversioned legacy connection data without lazy dual reads")
	must(p.applyConnectionMigration())
	p.checkConnectionStateIndexes()
	p.check(before == p.unrelatedAuthSnapshot(), "transactional connection migration preserves all unrelated auth rows")
	row := take(p.q.GetConnectionByUser(p.ctx, authdb.GetConnectionByUserParams{UserID: id(session.User.ID), ProviderID: "google"}))
	p.check(row.ID == connectionID && row.UserID == id(session.User.ID) && row.ProviderSubject == "google-subject" && row.Issuer == "https://accounts.google.com" && row.Status == "active" && row.Scopes == gmailModify && row.ConnectedAt.Equal(connected) && row.CreatedAt.Equal(connected) && row.UpdatedAt.Equal(connected), "legacy row ID, ownership, account, scopes, status and timestamps survive extension")
	p.check(bytes.Equal(row.RefreshTokenCiphertext, refresh) && bytes.Equal(row.AccessTokenCiphertext, access), "migration preserves exact ciphertext bytes")
	p.check(take(cipher.Open(row.RefreshTokenCiphertext)) == "legacy-refresh" && take(auth.ConnectionTokenForUser(p.ctx, session.User.ID, "google", gmailModify)) == "legacy-access", "the unchanged key decrypts migrated credentials in memory and the public worker returns the cached bearer")
	var loginConsumed, connectionConsumed sql.NullTime
	must(p.db.QueryRowContext(p.ctx, `SELECT consumed_at FROM scenery.scenery_auth_oauth_states WHERE id=$1`, loginState).Scan(&loginConsumed))
	must(p.db.QueryRowContext(p.ctx, `SELECT consumed_at FROM scenery.scenery_auth_oauth_states WHERE id=$1`, connectionState).Scan(&connectionConsumed))
	p.check(!loginConsumed.Valid && connectionConsumed.Valid, "only incompatible connection handshakes are retired; login state is preserved")
	var oldExists bool
	must(p.db.QueryRowContext(p.ctx, `SELECT to_regclass('scenery.scenery_auth_google_connections') IS NOT NULL`).Scan(&oldExists))
	p.check(!oldExists, "one canonical table replaces the legacy connection table")
	_, err = p.db.ExecContext(p.ctx, `DROP INDEX scenery.connection_states_expires_at_idx; DROP INDEX scenery.connection_states_pending_owner_provider_idx`)
	must(err)
	must(p.applyConnectionMigration())
	p.checkConnectionStateIndexes()
	rerun := take(p.q.GetConnectionByUser(p.ctx, authdb.GetConnectionByUserParams{UserID: id(session.User.ID), ProviderID: "google"}))
	p.check(rerun.ID == row.ID && bytes.Equal(rerun.RefreshTokenCiphertext, row.RefreshTokenCiphertext) && before == p.unrelatedAuthSnapshot(), "safe rerun changes no row identity, ciphertext or unrelated data")
}

func connectionMigrationRefusalJourney(p *probe) {
	_, _ = p.signin()
	must(authschema.CheckCurrentConnections(p.ctx, p.db))
	p.checkConnectionStateIndexes()
	p.check(true, "empty installation creates the exact versioned generic schema")
	p.installLegacyConnections()
	_, err := p.db.ExecContext(p.ctx, `ALTER TABLE scenery.scenery_auth_google_connections ADD COLUMN unknown_shape text`)
	must(err)
	p.check(p.applyConnectionMigration() != nil, "unknown legacy shape is refused without adoption")
	var legacy, current bool
	must(p.db.QueryRowContext(p.ctx, `SELECT to_regclass('scenery.scenery_auth_google_connections') IS NOT NULL,to_regclass('scenery.scenery_auth_connections') IS NOT NULL`).Scan(&legacy, &current))
	p.check(legacy && !current, "failed migration leaves only the original legacy table")
	_, err = p.db.ExecContext(p.ctx, `ALTER TABLE scenery.scenery_auth_google_connections DROP COLUMN unknown_shape; ALTER TABLE scenery.scenery_auth_google_connections DROP CONSTRAINT scenery_auth_google_connections_status_check`)
	must(err)
	p.check(p.applyConnectionMigration() != nil, "a failed alteration rolls back the earlier rename and added columns")
	must(p.db.QueryRowContext(p.ctx, `SELECT to_regclass('scenery.scenery_auth_google_connections') IS NOT NULL,to_regclass('scenery.scenery_auth_connections') IS NOT NULL`).Scan(&legacy, &current))
	p.check(legacy && !current, "mid-migration failure restores the original table identity")
	_, err = p.db.ExecContext(p.ctx, `ALTER TABLE scenery.scenery_auth_google_connections ADD CONSTRAINT scenery_auth_google_connections_status_check CHECK(status IN ('active','reauth_required','disconnected'))`)
	must(err)
	must(p.applyConnectionMigration())
	_, err = p.db.ExecContext(p.ctx, `ALTER TABLE scenery.scenery_auth_connection_states ADD COLUMN unknown_state text`)
	must(err)
	p.check(p.applyConnectionMigration() != nil, "rerun refuses an unknown current handshake shape")
	p.check(authschema.CheckConnectionBootstrap(p.ctx, p.db) != nil, "ordinary bootstrap also refuses unknown connection shape")
	_, err = p.db.ExecContext(p.ctx, `ALTER TABLE scenery.scenery_auth_connection_states DROP COLUMN unknown_state; ALTER TABLE scenery.scenery_auth_connection_states DROP CONSTRAINT scenery_auth_connection_states_session_id_fkey`)
	must(err)
	p.check(p.applyConnectionMigration() != nil && authschema.CheckConnectionBootstrap(p.ctx, p.db) != nil, "migration and bootstrap refuse missing live-session ownership constraints")
}

func (p *probe) checkConnectionStateIndexes() {
	var count int
	must(p.db.QueryRowContext(p.ctx, `SELECT count(*) FROM pg_index idx JOIN pg_class rel ON rel.oid=idx.indexrelid
WHERE idx.indrelid='scenery.scenery_auth_connection_states'::regclass AND idx.indisvalid AND idx.indisready AND idx.indexprs IS NULL
AND ((rel.relname='connection_states_expires_at_idx' AND idx.indnatts=1 AND pg_get_indexdef(idx.indexrelid,1,true)='expires_at' AND idx.indpred IS NULL)
OR (rel.relname='connection_states_pending_owner_provider_idx' AND idx.indnatts=2 AND pg_get_indexdef(idx.indexrelid,1,true)='user_id' AND pg_get_indexdef(idx.indexrelid,2,true)='provider_id' AND pg_get_expr(idx.indpred,idx.indrelid)='(consumed_at IS NULL)'))`).Scan(&count))
	p.check(count == 2, "clean schema and coordinated migration provide valid expiry and pending owner/provider callback indexes")
}
