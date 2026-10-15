-- Explicit coordinated upgrade. Run with psql --single-transaction and
-- ON_ERROR_STOP=1 only after an encrypted backup and quiescing every writer.
-- No old/new mixed writers are supported. Runtime bootstrap never runs this file.
SELECT pg_advisory_xact_lock(hashtextextended('scenery.standard.auth', 0));

DO $migration$
DECLARE legacy regclass := to_regclass('scenery.scenery_auth_google_connections');
        current_table regclass := to_regclass('scenery.scenery_auth_connections');
        marker regclass := to_regclass('scenery.scenery_auth_connection_schema');
        shape text;
        current_version integer;
        unique_owner boolean;
BEGIN
  IF legacy IS NULL THEN
    IF current_table IS NULL OR marker IS NULL OR to_regclass('scenery.scenery_auth_connection_states') IS NULL THEN
      RAISE EXCEPTION 'unknown connection schema; use the clean-install schema for an empty installation';
    END IF;
    SELECT version INTO current_version FROM scenery.scenery_auth_connection_schema WHERE singleton;
    SELECT string_agg(attname||':'||format_type(atttypid,atttypmod)||':'||attnotnull::text,',' ORDER BY attname)
      INTO shape FROM pg_attribute WHERE attrelid=current_table AND attnum>0 AND NOT attisdropped;
    IF current_version IS DISTINCT FROM 1 OR shape IS DISTINCT FROM 'access_token_ciphertext:bytea:false,access_token_expires_at:timestamp with time zone:false,connected_at:timestamp with time zone:true,created_at:timestamp with time zone:true,disconnected_at:timestamp with time zone:false,email:text:true,id:uuid:true,issuer:text:true,last_refresh_at:timestamp with time zone:false,last_refresh_error:text:true,provider_id:text:true,provider_subject:text:true,refresh_token_ciphertext:bytea:false,resource:text:true,revocation_status:text:true,scopes:text:true,status:text:true,token_type:text:true,updated_at:timestamp with time zone:true,user_id:uuid:true' THEN
      RAISE EXCEPTION 'unknown current connection schema';
    END IF;
    SELECT string_agg(attname||':'||format_type(atttypid,atttypmod)||':'||attnotnull::text,',' ORDER BY attname)
      INTO shape FROM pg_attribute WHERE attrelid='scenery.scenery_auth_connection_states'::regclass AND attnum>0 AND NOT attisdropped;
    IF shape IS DISTINCT FROM 'browser_hash:text:true,callback_uri:text:true,client_id:text:true,consumed_at:timestamp with time zone:false,created_at:timestamp with time zone:true,expires_at:timestamp with time zone:true,id:uuid:true,issuer:text:true,nonce_hash:text:true,pkce_verifier:text:true,provider_id:text:true,purpose:text:true,redirect_path:text:true,requested_scopes:text:true,resource:text:true,session_id:uuid:true,state_hash:text:true,user_id:uuid:true' THEN RAISE EXCEPTION 'unknown current connection handshake schema'; END IF;
    SELECT string_agg(attname||':'||format_type(atttypid,atttypmod)||':'||attnotnull::text,',' ORDER BY attname)
      INTO shape FROM pg_attribute WHERE attrelid=marker AND attnum>0 AND NOT attisdropped;
    IF shape IS DISTINCT FROM 'singleton:boolean:true,version:integer:true' THEN RAISE EXCEPTION 'unknown current connection version schema'; END IF;
    RETURN;
  END IF;
  IF current_table IS NOT NULL OR marker IS NOT NULL OR to_regclass('scenery.scenery_auth_connection_states') IS NOT NULL THEN
    RAISE EXCEPTION 'ambiguous connection schemas; no data was adopted or deleted';
  END IF;
  LOCK TABLE scenery.scenery_auth_google_connections IN ACCESS EXCLUSIVE MODE;
  SELECT string_agg(attname||':'||format_type(atttypid,atttypmod)||':'||attnotnull::text,',' ORDER BY attname)
    INTO shape FROM pg_attribute WHERE attrelid=legacy AND attnum>0 AND NOT attisdropped;
  IF shape IS DISTINCT FROM 'access_token_ciphertext:bytea:false,access_token_expires_at:timestamp with time zone:false,connected_at:timestamp with time zone:true,created_at:timestamp with time zone:true,disconnected_at:timestamp with time zone:false,email:text:true,id:uuid:true,last_refresh_at:timestamp with time zone:false,last_refresh_error:text:true,provider_subject:text:true,refresh_token_ciphertext:bytea:false,scopes:text:true,status:text:true,updated_at:timestamp with time zone:true,user_id:uuid:true' THEN
    RAISE EXCEPTION 'unknown legacy connection schema';
  END IF;
  SELECT EXISTS (
    SELECT 1 FROM pg_index idx WHERE idx.indrelid=legacy AND idx.indisunique AND idx.indisvalid
      AND idx.indpred IS NULL AND idx.indexprs IS NULL
      AND (SELECT string_agg(attr.attname,',' ORDER BY ord.ordinality)
        FROM unnest(idx.indkey) WITH ORDINALITY AS ord(attnum,ordinality)
        JOIN pg_attribute attr ON attr.attrelid=idx.indrelid AND attr.attnum=ord.attnum)='user_id')
    INTO unique_owner;
  IF NOT unique_owner THEN RAISE EXCEPTION 'unknown legacy connection ownership'; END IF;
  ALTER TABLE scenery.scenery_auth_google_connections RENAME TO scenery_auth_connections;
  ALTER TABLE scenery.scenery_auth_connections
    ADD COLUMN provider_id text NOT NULL DEFAULT 'google',
    ADD COLUMN issuer text NOT NULL DEFAULT 'https://accounts.google.com',
    ADD COLUMN resource text NOT NULL DEFAULT '',
    ADD COLUMN token_type text NOT NULL DEFAULT 'Bearer',
    ADD COLUMN revocation_status text NOT NULL DEFAULT '';
  ALTER TABLE scenery.scenery_auth_connections ALTER COLUMN provider_id DROP DEFAULT;
  ALTER TABLE scenery.scenery_auth_connections ALTER COLUMN issuer DROP DEFAULT;
  ALTER TABLE scenery.scenery_auth_connections DROP CONSTRAINT scenery_auth_google_connections_status_check;
  ALTER TABLE scenery.scenery_auth_connections ADD CONSTRAINT scenery_auth_connections_status_check
    CHECK (status IN ('active','reauth_required','disconnected','refresh_pending'));
  ALTER TABLE scenery.scenery_auth_connections RENAME CONSTRAINT scenery_auth_google_connections_pkey TO scenery_auth_connections_pkey;
  DROP INDEX scenery.google_connections_user_id_key;
  DROP INDEX scenery.google_connections_status_idx;
  CREATE UNIQUE INDEX connections_user_provider_key ON scenery.scenery_auth_connections (user_id,provider_id);
  CREATE INDEX connections_status_idx ON scenery.scenery_auth_connections (status);
  -- Retire only incompatible external-connection handshakes. Login state and
  -- every user/session/tenant/membership/identity remain untouched.
  UPDATE scenery.scenery_auth_oauth_states SET consumed_at=COALESCE(consumed_at,now()) WHERE purpose='google_connection';
END
$migration$;

CREATE TABLE IF NOT EXISTS scenery.scenery_auth_connection_states (
  id uuid PRIMARY KEY NOT NULL,
  state_hash text NOT NULL UNIQUE,
  purpose text NOT NULL DEFAULT 'connection' CHECK (purpose = 'connection'),
  user_id uuid NOT NULL REFERENCES scenery.scenery_auth_users (id) ON DELETE CASCADE,
  session_id uuid NOT NULL REFERENCES scenery.scenery_auth_refresh_sessions (id) ON DELETE CASCADE,
  provider_id text NOT NULL,
  issuer text NOT NULL,
  resource text NOT NULL DEFAULT '',
  client_id text NOT NULL,
  browser_hash text NOT NULL,
  callback_uri text NOT NULL,
  requested_scopes text NOT NULL,
  pkce_verifier text NOT NULL,
  nonce_hash text NOT NULL DEFAULT '',
  redirect_path text NOT NULL DEFAULT '',
  expires_at timestamptz NOT NULL,
  consumed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS connection_states_expires_at_idx ON scenery.scenery_auth_connection_states (expires_at);
CREATE INDEX IF NOT EXISTS connection_states_pending_owner_provider_idx ON scenery.scenery_auth_connection_states (user_id, provider_id) WHERE consumed_at IS NULL;

CREATE TABLE IF NOT EXISTS scenery.scenery_auth_connection_schema (
  singleton boolean PRIMARY KEY NOT NULL DEFAULT true CHECK (singleton),
  version integer NOT NULL CHECK (version = 1)
);
INSERT INTO scenery.scenery_auth_connection_schema (singleton,version) VALUES (true,1) ON CONFLICT (singleton) DO NOTHING;

DO $verify$
DECLARE item record; shape text; unique_owner boolean;
BEGIN
 FOR item IN SELECT * FROM (VALUES
  ('scenery.scenery_auth_connections', 'CHECK((status=ANY(ARRAY[''active''::text,''reauth_required''::text,''disconnected''::text,''refresh_pending''::text]))):true:false|FOREIGNKEY(user_id)REFERENCESscenery_auth_users(id)ONDELETECASCADE:true:false:scenery|PRIMARYKEY(id):true:false'),
  ('scenery.scenery_auth_connection_states', 'CHECK((purpose=''connection''::text)):true:false|FOREIGNKEY(session_id)REFERENCESscenery_auth_refresh_sessions(id)ONDELETECASCADE:true:false:scenery|FOREIGNKEY(user_id)REFERENCESscenery_auth_users(id)ONDELETECASCADE:true:false:scenery|PRIMARYKEY(id):true:false|UNIQUE(state_hash):true:false'),
  ('scenery.scenery_auth_connection_schema', 'CHECK((version=1)):true:false|CHECK(singleton):true:false|PRIMARYKEY(singleton):true:false')
 ) AS shapes(table_name, expected) LOOP
  SELECT COALESCE(string_agg(regexp_replace(replace(pg_get_constraintdef(oid), 'scenery.', ''), '\s', '', 'g')||':'||convalidated::text||':'||condeferrable::text||CASE WHEN contype='f' THEN ':'||(SELECT ns.nspname FROM pg_class rel JOIN pg_namespace ns ON ns.oid=rel.relnamespace WHERE rel.oid=confrelid) ELSE '' END, '|' ORDER BY contype, regexp_replace(replace(pg_get_constraintdef(oid), 'scenery.', ''), '\s', '', 'g') COLLATE "C"), '')
   INTO shape FROM pg_constraint WHERE conrelid=to_regclass(item.table_name) AND contype <> 'n';
  IF shape IS DISTINCT FROM item.expected THEN RAISE EXCEPTION 'unknown connection constraints'; END IF;
 END LOOP;
 SELECT EXISTS (SELECT 1 FROM pg_index idx WHERE idx.indrelid='scenery.scenery_auth_connections'::regclass AND idx.indisunique AND idx.indisvalid AND idx.indpred IS NULL AND idx.indexprs IS NULL
  AND (SELECT string_agg(attr.attname,',' ORDER BY ord.ordinality) FROM unnest(idx.indkey) WITH ORDINALITY AS ord(attnum,ordinality) JOIN pg_attribute attr ON attr.attrelid=idx.indrelid AND attr.attnum=ord.attnum)='user_id,provider_id') INTO unique_owner;
 IF NOT unique_owner THEN RAISE EXCEPTION 'unknown connection ownership'; END IF;
END
$verify$;
