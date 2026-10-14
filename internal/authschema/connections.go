// Package authschema checks framework connection schema ownership without
// importing the vault, providers or runtime into the public SDK.
package authschema

import (
	"context"
	"database/sql"
	"errors"
)

var ErrConnectionMigrationRequired = errors.New("standard auth connection schema requires the explicit auth/db/migrations/0001-provider-neutral-connections.sql migration with all token writers stopped")
var ErrUnknownConnectionSchema = errors.New("standard auth connection schema ownership or shape is unknown")

type Queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// CheckConnectionBootstrap permits only a new family or the exact current
// family. Ordinary bootstrap never silently creates a second connection table
// beside legacy data or adopts an unversioned populated table.
func CheckConnectionBootstrap(ctx context.Context, q Queryer) error {
	var legacy, current, marker, states bool
	if err := q.QueryRowContext(ctx, `-- name: ConnectionSchemaPresence :one
SELECT
 to_regclass('scenery.scenery_auth_google_connections') IS NOT NULL,
 to_regclass('scenery.scenery_auth_connections') IS NOT NULL,
 to_regclass('scenery.scenery_auth_connection_schema') IS NOT NULL,
 to_regclass('scenery.scenery_auth_connection_states') IS NOT NULL`).Scan(&legacy, &current, &marker, &states); err != nil {
		return err
	}
	if legacy {
		return ErrConnectionMigrationRequired
	}
	if !current && !marker && !states {
		return nil
	}
	if !current || !marker || !states {
		return ErrUnknownConnectionSchema
	}
	return CheckCurrentConnections(ctx, q)
}

const connectionColumns = "access_token_ciphertext:bytea:false,access_token_expires_at:timestamp with time zone:false,connected_at:timestamp with time zone:true,created_at:timestamp with time zone:true,disconnected_at:timestamp with time zone:false,email:text:true,id:uuid:true,issuer:text:true,last_refresh_at:timestamp with time zone:false,last_refresh_error:text:true,provider_id:text:true,provider_subject:text:true,refresh_token_ciphertext:bytea:false,resource:text:true,revocation_status:text:true,scopes:text:true,status:text:true,token_type:text:true,updated_at:timestamp with time zone:true,user_id:uuid:true"
const markerColumns = "singleton:boolean:true,version:integer:true"
const stateColumns = "browser_hash:text:true,callback_uri:text:true,client_id:text:true,consumed_at:timestamp with time zone:false,created_at:timestamp with time zone:true,expires_at:timestamp with time zone:true,id:uuid:true,issuer:text:true,nonce_hash:text:true,pkce_verifier:text:true,provider_id:text:true,purpose:text:true,redirect_path:text:true,requested_scopes:text:true,resource:text:true,session_id:uuid:true,state_hash:text:true,user_id:uuid:true"
const columnShapeSQL = `SELECT COALESCE(string_agg(attname||':'||format_type(atttypid,atttypmod)||':'||attnotnull::text,',' ORDER BY attname),'')
FROM pg_attribute WHERE attrelid=to_regclass($1) AND attnum>0 AND NOT attisdropped`

func CheckCurrentConnections(ctx context.Context, q Queryer) error {
	var version int
	if err := q.QueryRowContext(ctx, `SELECT version FROM scenery.scenery_auth_connection_schema WHERE singleton=true`).Scan(&version); err != nil || version != 1 {
		return ErrUnknownConnectionSchema
	}
	for _, item := range []struct{ name, shape string }{{"scenery.scenery_auth_connections", connectionColumns}, {"scenery.scenery_auth_connection_states", stateColumns}, {"scenery.scenery_auth_connection_schema", markerColumns}} {
		var shape string
		if err := q.QueryRowContext(ctx, columnShapeSQL, item.name).Scan(&shape); err != nil {
			return err
		}
		if shape != item.shape {
			return ErrUnknownConnectionSchema
		}
	}
	var uniqueKey bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS (
 SELECT 1 FROM pg_index idx WHERE idx.indrelid='scenery.scenery_auth_connections'::regclass
 AND idx.indisunique AND idx.indisvalid AND idx.indpred IS NULL AND idx.indexprs IS NULL
 AND (SELECT string_agg(attr.attname,',' ORDER BY ord.ordinality)
      FROM unnest(idx.indkey) WITH ORDINALITY AS ord(attnum,ordinality)
      JOIN pg_attribute attr ON attr.attrelid=idx.indrelid AND attr.attnum=ord.attnum)='user_id,provider_id')`).Scan(&uniqueKey); err != nil {
		return err
	}
	if !uniqueKey {
		return ErrUnknownConnectionSchema
	}
	for _, item := range []struct{ name, shape string }{
		{"scenery.scenery_auth_connections", connectionConstraints},
		{"scenery.scenery_auth_connection_states", stateConstraints},
		{"scenery.scenery_auth_connection_schema", markerConstraints},
	} {
		var shape string
		if err := q.QueryRowContext(ctx, constraintShapeSQL, item.name).Scan(&shape); err != nil {
			return err
		}
		if shape != item.shape {
			return ErrUnknownConnectionSchema
		}
	}
	return nil
}

const constraintShapeSQL = `SELECT COALESCE(string_agg(regexp_replace(replace(pg_get_constraintdef(oid), 'scenery.', ''), '\s', '', 'g')||':'||convalidated::text||':'||condeferrable::text||CASE WHEN contype='f' THEN ':'||(SELECT ns.nspname FROM pg_class rel JOIN pg_namespace ns ON ns.oid=rel.relnamespace WHERE rel.oid=confrelid) ELSE '' END, '|' ORDER BY contype, regexp_replace(replace(pg_get_constraintdef(oid), 'scenery.', ''), '\s', '', 'g') COLLATE "C"), '')
FROM pg_constraint WHERE conrelid=to_regclass($1) AND contype <> 'n'`
const connectionConstraints = "CHECK((status=ANY(ARRAY['active'::text,'reauth_required'::text,'disconnected'::text,'refresh_pending'::text]))):true:false|FOREIGNKEY(user_id)REFERENCESscenery_auth_users(id)ONDELETECASCADE:true:false:scenery|PRIMARYKEY(id):true:false"
const stateConstraints = "CHECK((purpose='connection'::text)):true:false|FOREIGNKEY(session_id)REFERENCESscenery_auth_refresh_sessions(id)ONDELETECASCADE:true:false:scenery|FOREIGNKEY(user_id)REFERENCESscenery_auth_users(id)ONDELETECASCADE:true:false:scenery|PRIMARYKEY(id):true:false|UNIQUE(state_hash):true:false"
const markerConstraints = "CHECK((version=1)):true:false|CHECK(singleton):true:false|PRIMARYKEY(singleton):true:false"
