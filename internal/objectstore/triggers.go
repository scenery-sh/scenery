package objectstore

import (
	"context"
	"fmt"
	"strings"
)

func (s *Store) EnableOutboxTriggers(ctx context.Context, actor Actor, tenantKey, objectName string) (*Object, error) {
	state, err := s.loadState(ctx, tenantKey, objectName)
	if err != nil {
		return nil, err
	}
	if err := s.perms.CanWriteObject(ctx, actor, objectRef(state)); err != nil {
		return nil, err
	}
	triggerName := outboxTriggerName(state.Object.ID)
	if state.Object.OutboxTriggersEnabled {
		present, err := s.outboxTriggerPresent(ctx, s.db, state.Object.TableName, triggerName)
		if err != nil {
			return nil, err
		}
		if present {
			return state.Object, nil
		}
	}

	fromVersion := state.Object.SchemaVersion
	toVersion := fromVersion + 1
	ddl := []string{
		recordChangeTriggerFunctionDDL(),
		dropObjectOutboxTriggerDDL(state.Object.TableName, triggerName),
		createObjectOutboxTriggerDDL(state, triggerName),
	}
	migrationID, err := s.startMigration(ctx, state.Tenant.ID, state.Object.ID, fromVersion, toVersion, ddl)
	if err != nil {
		return nil, err
	}
	var event *Event
	if err := s.withMigrationTx(ctx, state.Tenant.ID, state.Object.ID, migrationID, func(tx pgxTx) error {
		for _, stmt := range ddl {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("enable outbox trigger for object %s: %w", state.Object.NameSingular, err)
			}
		}
		present, err := s.outboxTriggerPresent(ctx, tx, state.Object.TableName, triggerName)
		if err != nil {
			return err
		}
		if !present {
			return fmt.Errorf("outbox trigger %s was not created on %s.%s", triggerName, RecordsSchema, state.Object.TableName)
		}
		if _, err := tx.Exec(ctx, `
			update `+qualifiedIdent(MetadataSchema, "objects")+`
			set outbox_triggers_enabled = true, schema_version = $1, updated_at = $2
			where id = $3
		`, toVersion, s.now(), state.Object.ID); err != nil {
			return err
		}
		var outboxErr error
		event, outboxErr = s.insertOutbox(ctx, tx, outboxDraft{
			TenantID:      state.Tenant.ID,
			ObjectID:      state.Object.ID,
			ObjectName:    state.Object.NameSingular,
			Action:        "object.outbox_triggers_enabled",
			ActorID:       actor.ID,
			SchemaVersion: toVersion,
			ChangedFields: []string{"outbox_triggers_enabled"},
			After: Record{
				"id":                      state.Object.ID,
				"name_singular":           state.Object.NameSingular,
				"outbox_triggers_enabled": true,
				"outbox_trigger":          triggerName,
			},
		})
		return outboxErr
	}); err != nil {
		_ = s.finishMigration(ctx, migrationID, "failed", err.Error())
		return nil, err
	}
	if err := s.finishMigration(ctx, migrationID, "applied", ""); err != nil {
		return nil, err
	}
	s.router.publish(event)
	return s.loadObject(ctx, state.Tenant.ID, state.Object.NameSingular)
}

func setOutboxTxContext(ctx context.Context, tx Queryer, actor Actor, explicit bool) error {
	if _, err := tx.Exec(ctx, `select set_config('onlava.actor_id', $1, true)`, actor.ID); err != nil {
		return err
	}
	if explicit {
		_, err := tx.Exec(ctx, `select set_config('onlava.outbox_explicit', 'true', true)`)
		return err
	}
	_, err := tx.Exec(ctx, `select set_config('onlava.outbox_explicit', '', true)`)
	return err
}

func (s *Store) outboxTriggerPresent(ctx context.Context, q Queryer, tableName, triggerName string) (bool, error) {
	var present bool
	err := q.QueryRow(ctx, `
		select exists (
			select 1
			from pg_trigger tr
			join pg_class c on c.oid = tr.tgrelid
			join pg_namespace n on n.oid = c.relnamespace
			where n.nspname = $1
			  and c.relname = $2
			  and tr.tgname = $3
			  and not tr.tgisinternal
		)
	`, RecordsSchema, tableName, triggerName).Scan(&present)
	return present, err
}

func outboxTriggerName(objectID string) string {
	return physicalNameWithSuffix("outbox", shortIdentifierSuffix(objectID))
}

func dropObjectOutboxTriggerDDL(tableName, triggerName string) string {
	return `drop trigger if exists ` + quoteIdent(triggerName) + ` on ` + qualifiedIdent(RecordsSchema, tableName)
}

func createObjectOutboxTriggerDDL(state *metadataState, triggerName string) string {
	return `create trigger ` + quoteIdent(triggerName) + `
after insert or update or delete on ` + qualifiedIdent(RecordsSchema, state.Object.TableName) + `
for each row execute function ` + qualifiedIdent(MetadataSchema, "record_change_trigger") + `(` +
		quoteLiteral(state.Object.ID) + `, ` +
		quoteLiteral(state.Tenant.ID) + `, ` +
		quoteLiteral(state.Object.NameSingular) + `)`
}

func quoteLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}

func recordChangeTriggerFunctionDDL() string {
	return `create or replace function ` + qualifiedIdent(MetadataSchema, "record_change_trigger") + `()
returns trigger
language plpgsql
as $$
declare
	v_object_id uuid := TG_ARGV[0]::uuid;
	v_default_tenant_id uuid := TG_ARGV[1]::uuid;
	v_object_name text := TG_ARGV[2];
	v_schema_version bigint := 0;
	v_actor_id text := coalesce(current_setting('onlava.actor_id', true), '');
	v_before_raw jsonb;
	v_after_raw jsonb;
	v_before_logical jsonb;
	v_after_logical jsonb;
	v_before_value jsonb;
	v_after_value jsonb;
	v_diff jsonb := '{}'::jsonb;
	v_field record;
	v_col record;
	v_columns jsonb;
	v_col_name text;
	v_part text;
	v_record_id uuid;
	v_tenant_id uuid;
	v_action text;
	v_changed_fields text[] := '{}'::text[];
	v_hash text;
	v_event_id uuid;
begin
	if coalesce(current_setting('onlava.outbox_explicit', true), '') in ('true', '1', 'yes') then
		if TG_OP = 'DELETE' then
			return OLD;
		end if;
		return NEW;
	end if;

	if TG_OP = 'INSERT' then
		v_action := 'created';
		v_after_raw := to_jsonb(NEW);
	elsif TG_OP = 'UPDATE' then
		v_action := 'updated';
		v_before_raw := to_jsonb(OLD);
		v_after_raw := to_jsonb(NEW);
	elsif TG_OP = 'DELETE' then
		v_action := 'deleted';
		v_before_raw := to_jsonb(OLD);
	else
		raise exception 'unsupported trigger operation %', TG_OP;
	end if;

	v_record_id := coalesce((v_after_raw->>'id')::uuid, (v_before_raw->>'id')::uuid);
	v_tenant_id := coalesce((v_after_raw->>'tenant_id')::uuid, (v_before_raw->>'tenant_id')::uuid, v_default_tenant_id);

	select schema_version
	into v_schema_version
	from ` + qualifiedIdent(MetadataSchema, "objects") + `
	where id = v_object_id;
	v_schema_version := coalesce(v_schema_version, 0);

	if v_before_raw is not null then
		v_before_logical := jsonb_build_object(
			'id', v_before_raw->'id',
			'created_at', v_before_raw->'created_at',
			'updated_at', v_before_raw->'updated_at',
			'deleted_at', v_before_raw->'deleted_at'
		);
	end if;
	if v_after_raw is not null then
		v_after_logical := jsonb_build_object(
			'id', v_after_raw->'id',
			'created_at', v_after_raw->'created_at',
			'updated_at', v_after_raw->'updated_at',
			'deleted_at', v_after_raw->'deleted_at'
		);
	end if;

	for v_field in
		select name, storage_columns
		from ` + qualifiedIdent(MetadataSchema, "fields") + `
		where object_id = v_object_id
		order by name
	loop
		v_columns := coalesce(v_field.storage_columns, '[]'::jsonb);
		v_before_value := null;
		v_after_value := null;

		if jsonb_typeof(v_columns) = 'array' and jsonb_array_length(v_columns) = 1 and coalesce(v_columns->0->>'part', '') = '' then
			v_col_name := v_columns->0->>'name';
			if v_before_raw is not null then
				v_before_value := coalesce(v_before_raw -> v_col_name, 'null'::jsonb);
				v_before_logical := jsonb_set(v_before_logical, array[v_field.name], v_before_value, true);
			end if;
			if v_after_raw is not null then
				v_after_value := coalesce(v_after_raw -> v_col_name, 'null'::jsonb);
				v_after_logical := jsonb_set(v_after_logical, array[v_field.name], v_after_value, true);
			end if;
		else
			v_before_value := '{}'::jsonb;
			v_after_value := '{}'::jsonb;
			for v_col in select value from jsonb_array_elements(v_columns)
			loop
				v_col_name := v_col.value->>'name';
				v_part := coalesce(v_col.value->>'part', v_col_name);
				if v_before_raw is not null then
					v_before_value := jsonb_set(v_before_value, array[v_part], coalesce(v_before_raw -> v_col_name, 'null'::jsonb), true);
				end if;
				if v_after_raw is not null then
					v_after_value := jsonb_set(v_after_value, array[v_part], coalesce(v_after_raw -> v_col_name, 'null'::jsonb), true);
				end if;
			end loop;
			if v_before_raw is not null then
				v_before_logical := jsonb_set(v_before_logical, array[v_field.name], v_before_value, true);
			end if;
			if v_after_raw is not null then
				v_after_logical := jsonb_set(v_after_logical, array[v_field.name], v_after_value, true);
			end if;
		end if;

		if v_action = 'created' then
			v_changed_fields := array_append(v_changed_fields, v_field.name);
			v_diff := jsonb_set(v_diff, array[v_field.name], coalesce(v_after_value, 'null'::jsonb), true);
		elsif v_action = 'updated' and v_before_value is distinct from v_after_value then
			v_changed_fields := array_append(v_changed_fields, v_field.name);
			v_diff := jsonb_set(v_diff, array[v_field.name], coalesce(v_after_value, 'null'::jsonb), true);
		elsif v_action = 'deleted' then
			v_changed_fields := array_append(v_changed_fields, v_field.name);
		end if;
	end loop;

	if v_action = 'deleted' then
		v_diff := jsonb_build_object('deleted', true);
	end if;
	v_hash := md5(random()::text || clock_timestamp()::text || txid_current()::text || coalesce(v_record_id::text, ''));
	v_event_id := (
		substr(v_hash, 1, 8) || '-' ||
		substr(v_hash, 9, 4) || '-4' ||
		substr(v_hash, 14, 3) || '-' ||
		substr('89ab', floor(random() * 4)::int + 1, 1) ||
		substr(v_hash, 18, 3) || '-' ||
		substr(v_hash, 21, 12)
	)::uuid;

	insert into ` + qualifiedIdent(MetadataSchema, "outbox_events") + ` (
		id, tenant_id, object_id, object_name, record_id, action, actor_id,
		schema_version, changed_fields, before, after, diff, created_at
	) values (
		v_event_id, v_tenant_id, v_object_id, v_object_name, v_record_id, v_action, v_actor_id,
		v_schema_version, coalesce(v_changed_fields, '{}'::text[]), v_before_logical, v_after_logical, v_diff, now()
	);

	if TG_OP = 'DELETE' then
		return OLD;
	end if;
	return NEW;
end;
$$`
}
